package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"expense-tracker/db"
	apperrors "expense-tracker/errors"
	"expense-tracker/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxBatchSize caps one ingest call. The macOS backfill can offer months of
// history at once; chunking it keeps a single INSERT (and a single failure)
// bounded, and keeps the request body well inside the transport limit.
const MaxBatchSize = 500

// MessageService owns the ingestion side of the pipeline: taking raw bank SMS
// from the client, classifying them and persisting them as RawMessage rows.
// Later stages (parsing, validation, categorization) will consume these rows.
type MessageService struct {
	detector *TransactionDetector
}

func NewMessageService() *MessageService {
	return &MessageService{detector: NewTransactionDetector()}
}

// IngestInput is a single inbound message as received from the client.
type IngestInput struct {
	Sender     string
	Body       string
	Source     model.MessageSource
	ReceivedAt time.Time

	// ExternalID is the source's own stable id for this message (chat.db's
	// message.guid). Optional: when set it is the dedup key, otherwise the
	// content hash is.
	ExternalID string
}

// IngestResult reports what happened to one message.
type IngestResult struct {
	Message   *model.RawMessage
	Duplicate bool // true if an identical raw message already existed
}

// Ingest persists a single raw message for a user. It is idempotent: if the
// same message was already stored, the existing row is returned with
// Duplicate=true instead of creating a copy.
func (s *MessageService) Ingest(userID string, in IngestInput) (*IngestResult, error) {
	results, err := s.IngestBatch(userID, []IngestInput{in})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// IngestBatch persists many messages, returning a result per input in order.
//
// It is one multi-row INSERT (two, when the batch mixes messages with and
// without an external id) plus one SELECT, rather than a round-trip per
// message: against a pooled serverless Postgres the per-statement latency
// dominates, so a few thousand backfilled messages is the difference between
// seconds and minutes.
func (s *MessageService) IngestBatch(userID string, inputs []IngestInput) ([]*IngestResult, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", apperrors.ErrInvalidInput)
	}
	if len(inputs) == 0 {
		return []*IngestResult{}, nil
	}
	if len(inputs) > MaxBatchSize {
		return nil, fmt.Errorf("%w: batch contains %d messages, the maximum is %d",
			apperrors.ErrInvalidInput, len(inputs), MaxBatchSize)
	}

	// rows[i] is the message built for inputs[i]; repeats within the batch
	// share the row of their first occurrence so we never offer the same
	// dedup key twice to one INSERT.
	rows := make([]*model.RawMessage, len(inputs))
	repeat := make([]bool, len(inputs))
	firstSeen := make(map[string]*model.RawMessage, len(inputs))
	unique := make([]*model.RawMessage, 0, len(inputs))

	for i, in := range inputs {
		msg, err := s.buildMessage(userID, in)
		if err != nil {
			return nil, err
		}
		key := dedupKey(msg)
		if prior, ok := firstSeen[key]; ok {
			rows[i] = prior
			repeat[i] = true
			continue
		}
		firstSeen[key] = msg
		rows[i] = msg
		unique = append(unique, msg)
	}

	if err := s.insert(unique); err != nil {
		return nil, err
	}

	stored, err := s.lookupStored(userID, unique)
	if err != nil {
		return nil, err
	}

	results := make([]*IngestResult, len(inputs))
	for i, msg := range rows {
		row, ok := stored[dedupKey(msg)]
		if !ok {
			// The row we just inserted (or conflicted against) isn't there.
			// Reporting success would tell the sync tool to advance its
			// watermark past a message we don't have.
			return nil, fmt.Errorf("%w: message disappeared after insert", apperrors.ErrInternal)
		}
		results[i] = &IngestResult{
			Message: row,
			// We generate ids client-side, so a stored row carrying an id we
			// didn't just mint is proof the message already existed. A repeat
			// within this batch is a duplicate submission too, even though its
			// first occurrence was freshly inserted.
			Duplicate: repeat[i] || row.ID != msg.ID,
		}
	}
	return results, nil
}

// insert writes the new rows. The two dedup keys need separate statements
// because ON CONFLICT takes a single conflict target, and their unique indexes
// are partial (each covers only the rows the other doesn't), so a row can never
// collide with the index it didn't declare.
func (s *MessageService) insert(msgs []*model.RawMessage) error {
	withExternalID := make([]*model.RawMessage, 0, len(msgs))
	withoutExternalID := make([]*model.RawMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.ExternalID != "" {
			withExternalID = append(withExternalID, m)
		} else {
			withoutExternalID = append(withoutExternalID, m)
		}
	}

	if err := s.insertOnConflict(withExternalID, clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "external_id"}},
		// Postgres only infers a partial index when the statement repeats its
		// predicate, so this WHERE is required, not decorative.
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "external_id <> ''"}}},
		DoNothing:   true,
	}); err != nil {
		return err
	}

	return s.insertOnConflict(withoutExternalID, clause.OnConflict{
		Columns:     []clause.Column{{Name: "content_hash"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "external_id = ''"}}},
		DoNothing:   true,
	})
}

func (s *MessageService) insertOnConflict(msgs []*model.RawMessage, onConflict clause.OnConflict) error {
	if len(msgs) == 0 {
		return nil
	}
	if err := db.WriteConnection().Clauses(onConflict).Create(&msgs).Error; err != nil {
		return fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}
	return nil
}

// lookupStored fetches the authoritative row for every dedup key in the batch,
// keyed the same way. It reads the write connection on purpose: a replica may
// not have the rows we inserted a millisecond ago, and every row here is one we
// just wrote.
func (s *MessageService) lookupStored(userID string, msgs []*model.RawMessage) (map[string]*model.RawMessage, error) {
	externalIDs := make([]string, 0, len(msgs))
	hashes := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ExternalID != "" {
			externalIDs = append(externalIDs, m.ExternalID)
		} else {
			hashes = append(hashes, m.ContentHash)
		}
	}

	conn := db.WriteConnection()
	q := conn.Where("user_id = ?", userID)
	switch {
	case len(externalIDs) > 0 && len(hashes) > 0:
		q = q.Where(conn.Where("external_id IN ?", externalIDs).Or("content_hash IN ?", hashes))
	case len(externalIDs) > 0:
		q = q.Where("external_id IN ?", externalIDs)
	default:
		q = q.Where("content_hash IN ?", hashes)
	}

	var found []model.RawMessage
	if err := q.Find(&found).Error; err != nil {
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}

	stored := make(map[string]*model.RawMessage, len(found))
	for i := range found {
		stored[dedupKey(&found[i])] = &found[i]
	}
	return stored, nil
}

// buildMessage validates and normalises one input into a row ready to insert.
// The id is assigned here rather than in BeforeCreate so the caller can tell,
// after a conflicting insert, which rows it actually wrote.
func (s *MessageService) buildMessage(userID string, in IngestInput) (*model.RawMessage, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, fmt.Errorf("%w: message body is required", apperrors.ErrInvalidInput)
	}

	source := in.Source
	if source == "" {
		source = model.SourceIOSShortcut
	}
	if !source.Valid() {
		return nil, fmt.Errorf("%w: unknown source %q", apperrors.ErrInvalidInput, source)
	}

	receivedAt := in.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	receivedAt = receivedAt.UTC()

	sender := strings.TrimSpace(in.Sender)

	// Classify before the write so a non-transaction never enters the parser's
	// queue. Only Status is affected — Body is stored exactly as received, so
	// a REJECTED row can be re-classified later without re-fetching anything.
	status := model.StatusReceived
	if !s.detector.Classify(sender, body).IsTransaction {
		status = model.StatusRejected
	}

	return &model.RawMessage{
		ID:          uuid.New(),
		UserID:      userID,
		Sender:      sender,
		Body:        body,
		Source:      source,
		ReceivedAt:  receivedAt,
		Status:      status,
		ContentHash: contentHash(userID, sender, body, receivedAt),
		ExternalID:  strings.TrimSpace(in.ExternalID),
	}, nil
}

// SyncState is the watermark a pull-based sync tool needs to resume. It lets
// the macOS tool be deleted and rebuilt from scratch without re-sending months
// of history: it asks us what we last saw and starts from there.
type SyncState struct {
	Source         model.MessageSource `json:"source,omitempty"`
	LastReceivedAt *time.Time          `json:"last_received_at"`
	LastExternalID string              `json:"last_external_id"`
	Count          int64               `json:"count"`
}

// SyncState reports what we hold for a user, optionally narrowed to one source.
// A user with nothing stored gets a zero state rather than a 404 — "I have
// none of your messages" is a valid answer to "where did we leave off".
func (s *MessageService) SyncState(userID string, source model.MessageSource) (*SyncState, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: user id is required", apperrors.ErrInvalidInput)
	}
	if source != "" && !source.Valid() {
		return nil, fmt.Errorf("%w: unknown source %q", apperrors.ErrInvalidInput, source)
	}

	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Model(&model.RawMessage{}).Where("user_id = ?", userID)
		if source != "" {
			q = q.Where("source = ?", source)
		}
		return q
	}

	state := &SyncState{Source: source}

	if err := scope(db.ReadConnection()).Count(&state.Count).Error; err != nil {
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}
	if state.Count == 0 {
		return state, nil
	}

	// id breaks ties so two messages sharing a timestamp don't make the
	// watermark jitter between calls.
	var newest model.RawMessage
	err := scope(db.ReadConnection()).
		Order("received_at DESC, id DESC").
		First(&newest).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return state, nil
		}
		return nil, fmt.Errorf("%w: %v", apperrors.ErrInternal, err)
	}

	receivedAt := newest.ReceivedAt.UTC()
	state.LastReceivedAt = &receivedAt
	state.LastExternalID = newest.ExternalID
	return state, nil
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

// dedupKey is the identity a message is deduplicated on. The prefix keeps the
// two key spaces from ever colliding with each other.
func dedupKey(m *model.RawMessage) string {
	if m.ExternalID != "" {
		return "ext:" + m.ExternalID
	}
	return "hash:" + m.ContentHash
}

// contentHash builds the fallback idempotency key, used when the source has no
// id of its own. received_at is truncated to the second so trivial sub-second
// jitter from the client doesn't defeat dedup.
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
