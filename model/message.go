package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MessageStatus models the transaction lifecycle described in the product spec.
// A message enters as RECEIVED and moves forward through the pipeline; the
// exceptional states are terminal branches.
type MessageStatus string

const (
	// Primary pipeline states.
	StatusReceived    MessageStatus = "RECEIVED"    // raw SMS stored, nothing processed yet
	StatusParsed      MessageStatus = "PARSED"      // sender + fields extracted
	StatusValidated   MessageStatus = "VALIDATED"   // passed the transaction eligibility engine
	StatusCategorized MessageStatus = "CATEGORIZED" // assigned a spending category
	StatusConfirmed   MessageStatus = "CONFIRMED"   // user confirmed / auto-confirmed

	// Exceptional states.
	StatusRejected    MessageStatus = "REJECTED"     // not a transaction (balance/OTP/promo/etc.)
	StatusDuplicate   MessageStatus = "DUPLICATE"    // fingerprint matched an existing transaction
	StatusReversed    MessageStatus = "REVERSED"     // linked to a reversal
	StatusRefunded    MessageStatus = "REFUNDED"     // linked to a refund
	StatusNeedsReview MessageStatus = "NEEDS_REVIEW" // low confidence, awaiting user
)

// MessageSource records how the message reached us. iOS cannot read the SMS
// inbox freely, so the primary path is an iOS Shortcut posting to the API.
type MessageSource string

const (
	SourceIOSShortcut MessageSource = "ios_shortcut"
	SourceAndroid     MessageSource = "android"
	SourceManual      MessageSource = "manual"
	SourceAPI         MessageSource = "api"
)

// RawMessage is the first-class record of an inbound bank SMS. It is the input
// to the whole pipeline (detection -> parsing -> validation -> ...). We keep the
// original text verbatim so re-processing is always possible after parser
// improvements. Downstream tables (transactions, etc.) will reference this row.
type RawMessage struct {
	ID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID string    `gorm:"type:text;not null;index" json:"user_id"`

	Sender     string        `gorm:"type:text;index" json:"sender"`      // SMS sender id, e.g. "AD-HDFCBK"
	Body       string        `gorm:"type:text;not null" json:"body"`     // raw SMS text
	Source     MessageSource `gorm:"type:text;not null" json:"source"`   // ingestion channel
	ReceivedAt time.Time     `gorm:"not null;index" json:"received_at"`  // when the SMS arrived on device

	Status MessageStatus `gorm:"type:text;not null;index" json:"status"`

	// ContentHash is a sha256 over (user, sender, body, received_at). It gives us
	// cheap idempotency at ingestion so retries from the Shortcut don't create
	// duplicate raw rows. Semantic duplicate detection (same real-world
	// transaction across different messages) happens later in the pipeline.
	ContentHash string `gorm:"type:text;not null;uniqueIndex" json:"content_hash"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (RawMessage) TableName() string {
	return "raw_messages"
}

// BeforeCreate assigns a UUID if one was not set explicitly.
func (m *RawMessage) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}
