package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	apperrors "expense-tracker/errors"
	"expense-tracker/db"
	"expense-tracker/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MessageService owns the ingestion side of the pipeline: taking raw bank SMS
// from the client and persisting them as RawMessage rows in the RECEIVED state.
// Later stages (parsing, validation, categorization) will consume these rows.
type MessageService struct{}

func NewMessageService() *MessageService {
	return &MessageService{}
}

// IngestInput is a single inbound message as received from the client.
type IngestInput struct {
	Sender     string
	Body       string
	Source     model.MessageSource
	ReceivedAt time.Time
}

// IngestResult reports what happened to one message.
type IngestResult struct {
	Message   *model.RawMessage
	Duplicate bool // true if an identical raw message already existed
}

// Ingest persists a single raw message for a user. It is idempotent: if the
// exact same message (same user, sender, body, received_at) was already stored,
// the existing row is returned with Duplicate=true instead of creating a copy.
func (s *MessageService) Ingest(userID string, in IngestInput) (*IngestResult, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", apperrors.ErrInvalidInput)
	}

	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, fmt.Errorf("%w: message body is required", apperrors.ErrInvalidInput)
	}

	source := in.Source
	if source == "" {
		source = model.SourceIOSShortcut
	}

	receivedAt := in.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	receivedAt = receivedAt.UTC()

	sender := strings.TrimSpace(in.Sender)

	msg := &model.RawMessage{
		UserID:      userID,
		Sender:      sender,
		Body:        body,
		Source:      source,
		ReceivedAt:  receivedAt,
		Status:      model.StatusReceived,
		ContentHash: contentHash(userID, sender, body, receivedAt),
	}

	// Insert; on content_hash conflict do nothing so ingestion is idempotent.
	res := db.WriteConnection().
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "content_hash"}},
			DoNothing: true,
		}).
		Create(msg)

	if res.Error != nil {
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, res.Error)
	}

	// RowsAffected == 0 means the conflict fired: an identical message exists.
	if res.RowsAffected == 0 {
		existing, err := s.getByContentHash(userID, msg.ContentHash)
		if err != nil {
			return nil, err
		}
		return &IngestResult{Message: existing, Duplicate: true}, nil
	}

	return &IngestResult{Message: msg, Duplicate: false}, nil
}

// IngestBatch persists many messages, returning a result per input in order.
func (s *MessageService) IngestBatch(userID string, inputs []IngestInput) ([]*IngestResult, error) {
	results := make([]*IngestResult, 0, len(inputs))
	for _, in := range inputs {
		r, err := s.Ingest(userID, in)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// GetByID fetches a single raw message scoped to the owning user.
func (s *MessageService) GetByID(userID, id string) (*model.RawMessage, error) {
	var msg model.RawMessage
	err := db.ReadConnection().
		Where("id = ? AND user_id = ?", id, userID).
		First(&msg).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}
	return &msg, nil
}

// List returns raw messages for a user, newest first, optionally filtered by
// status. limit is clamped to a sane range.
func (s *MessageService) List(userID string, status model.MessageStatus, limit int) ([]model.RawMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	q := db.ReadConnection().
		Where("user_id = ?", userID).
		Order("received_at DESC").
		Limit(limit)

	if status != "" {
		q = q.Where("status = ?", status)
	}

	var msgs []model.RawMessage
	if err := q.Find(&msgs).Error; err != nil {
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}
	return msgs, nil
}

func (s *MessageService) getByContentHash(userID, hash string) (*model.RawMessage, error) {
	var msg model.RawMessage
	err := db.ReadConnection().
		Where("user_id = ? AND content_hash = ?", userID, hash).
		First(&msg).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}
	return &msg, nil
}

// contentHash builds the idempotency key for a raw message. received_at is
// truncated to the second so trivial sub-second jitter from the client doesn't
// defeat dedup.
func contentHash(userID, sender, body string, receivedAt time.Time) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d",
		userID,
		strings.ToUpper(sender),
		body,
		receivedAt.Truncate(time.Second).Unix(),
	)
	return hex.EncodeToString(h.Sum(nil))
}
