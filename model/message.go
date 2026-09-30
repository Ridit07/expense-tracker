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
	// SourceMacOSChatDB is a macOS companion tool reading the Messages
	// database directly. It is the only path that can backfill months of
	// history, which is why it also carries a stable per-message id.
	SourceMacOSChatDB MessageSource = "macos_chatdb"
)

// messageSources is the closed set of accepted values. Without it a typo in a
// client payload becomes a stored source that no later stage knows how to
// interpret, and nothing surfaces the mistake.
var messageSources = map[MessageSource]struct{}{
	SourceIOSShortcut: {},
	SourceAndroid:     {},
	SourceManual:      {},
	SourceAPI:         {},
	SourceMacOSChatDB: {},
}

// Valid reports whether s is a known ingestion source.
func (s MessageSource) Valid() bool {
	_, ok := messageSources[s]
	return ok
}

// MessageSources lists the accepted sources in a stable order, for error
// messages and documentation.
func MessageSources() []MessageSource {
	return []MessageSource{
		SourceIOSShortcut,
		SourceAndroid,
		SourceManual,
		SourceAPI,
		SourceMacOSChatDB,
	}
}

// RawMessage is the first-class record of an inbound bank SMS. It is the input
// to the whole pipeline (detection -> parsing -> validation -> ...). We keep the
// original text verbatim so re-processing is always possible after parser
// improvements. Downstream tables (transactions, etc.) will reference this row.
type RawMessage struct {
	ID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	UserID string    `gorm:"type:text;not null;index;uniqueIndex:idx_raw_messages_user_external_id,priority:1,where:external_id <> ''" json:"user_id"`

	Sender     string        `gorm:"type:text;index" json:"sender"`      // SMS sender id, e.g. "AD-HDFCBK"
	Body       string        `gorm:"type:text;not null" json:"body"`     // raw SMS text
	Source     MessageSource `gorm:"type:text;not null" json:"source"`   // ingestion channel
	ReceivedAt time.Time     `gorm:"not null;index" json:"received_at"`  // when the SMS arrived on device

	Status MessageStatus `gorm:"type:text;not null;index" json:"status"`

	// ContentHash is a sha256 over (user, sender, body, received_at). It is the
	// fallback idempotency key, used when the source gives us no id of its own,
	// so retries from the Shortcut don't create duplicate raw rows. Its unique
	// index is partial so it only governs rows without an ExternalID: the two
	// dedup keys stay in disjoint domains and a single insert can therefore
	// never trip a unique index other than the one it declares as its conflict
	// target.
	ContentHash string `gorm:"type:text;not null;uniqueIndex:idx_raw_messages_content_hash,where:external_id = ''" json:"content_hash"`

	// ExternalID is the source system's own stable id for the message —
	// chat.db's message.guid for the macOS path. When present it is a far
	// better dedup key than ContentHash: it survives re-reads of the same
	// database and edits to how we normalise the body. Empty means "the source
	// has no id for this", not "unknown", so it is stored as '' rather than
	// NULL; the (user_id, external_id) unique index is partial on <> '' so the
	// many id-less rows don't all collide with each other.
	// The column is written explicitly on every insert (never left to a DB
	// default) so the partial indexes above always see '' and never NULL.
	ExternalID string `gorm:"type:text;uniqueIndex:idx_raw_messages_user_external_id,priority:2" json:"external_id,omitempty"`

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
