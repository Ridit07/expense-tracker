package services

import (
	"strconv"
	"strings"
	"testing"
	"time"

	apperrors "expense-tracker/errors"
	"expense-tracker/model"

	"github.com/google/uuid"
)

func TestContentHashDeterministic(t *testing.T) {
	ts := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)

	a := contentHash("user-1", "AD-HDFCBK", "Rs. 1250 debited", ts)
	b := contentHash("user-1", "AD-HDFCBK", "Rs. 1250 debited", ts)

	if a != b {
		t.Fatalf("hash not deterministic: %s != %s", a, b)
	}
}

func TestContentHashIgnoresSubSecondJitter(t *testing.T) {
	base := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)
	jitter := base.Add(500 * time.Millisecond)

	if contentHash("u", "S", "body", base) != contentHash("u", "S", "body", jitter) {
		t.Fatal("sub-second jitter should not change the hash")
	}
}

func TestContentHashSenderCaseInsensitive(t *testing.T) {
	ts := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)

	if contentHash("u", "hdfcbk", "body", ts) != contentHash("u", "HDFCBK", "body", ts) {
		t.Fatal("sender casing should not change the hash")
	}
}

func TestContentHashDistinctInputs(t *testing.T) {
	ts := time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)

	if contentHash("u", "S", "body A", ts) == contentHash("u", "S", "body B", ts) {
		t.Fatal("different bodies should produce different hashes")
	}
	if contentHash("u1", "S", "body", ts) == contentHash("u2", "S", "body", ts) {
		t.Fatal("different users should produce different hashes")
	}
}

func TestBuildMessageDefaultsAndNormalises(t *testing.T) {
	svc := NewMessageService()

	msg, err := svc.buildMessage("user-1", IngestInput{
		Sender: "  AD-HDFCBK ",
		Body:   "  HDFC Bank: Rs. 1,250.00 debited from A/c XX1234 towards ZOMATO.  ",
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	if msg.Source != model.SourceIOSShortcut {
		t.Fatalf("source = %q, want the ios_shortcut default", msg.Source)
	}
	if msg.Sender != "AD-HDFCBK" {
		t.Fatalf("sender = %q, want it trimmed", msg.Sender)
	}
	if strings.HasPrefix(msg.Body, " ") || strings.HasSuffix(msg.Body, " ") {
		t.Fatalf("body = %q, want it trimmed", msg.Body)
	}
	if msg.ReceivedAt.IsZero() {
		t.Fatal("received_at should be server-stamped when the client omits it")
	}
	if msg.ID == uuid.Nil {
		t.Fatal("id should be assigned before insert so duplicates can be told apart")
	}
}

func TestBuildMessageRejectsEmptyBody(t *testing.T) {
	if _, err := NewMessageService().buildMessage("user-1", IngestInput{Body: "   "}); err == nil {
		t.Fatal("expected an error for a blank body")
	}
}

func TestBuildMessageRejectsUnknownSource(t *testing.T) {
	_, err := NewMessageService().buildMessage("user-1", IngestInput{
		Body:   "Rs. 10 debited",
		Source: model.MessageSource("macos_chatdbb"),
	})
	if err == nil {
		t.Fatal("expected an error for a typo'd source")
	}
	if !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestBuildMessageAcceptsMacOSChatDB(t *testing.T) {
	msg, err := NewMessageService().buildMessage("user-1", IngestInput{
		Body:       "Rs. 10 debited from A/c XX1",
		Source:     model.SourceMacOSChatDB,
		ExternalID: "  8A1C-GUID  ",
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if msg.ExternalID != "8A1C-GUID" {
		t.Fatalf("external_id = %q, want it trimmed", msg.ExternalID)
	}
}

// The detector runs inline so a non-transaction never reaches the parser, but
// it only moves the status: the body is what makes re-classification possible.
func TestBuildMessageRejectsNonTransactionsKeepingBodyVerbatim(t *testing.T) {
	body := "123456 is your OTP for login. Do not share it with anyone."

	msg, err := NewMessageService().buildMessage("user-1", IngestInput{Body: body})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if msg.Status != model.StatusRejected {
		t.Fatalf("status = %q, want REJECTED for an OTP", msg.Status)
	}
	if msg.Body != body {
		t.Fatalf("body = %q, want it stored verbatim", msg.Body)
	}
}

func TestBuildMessageKeepsTransactionsReceived(t *testing.T) {
	msg, err := NewMessageService().buildMessage("user-1", IngestInput{
		Body: "HDFC Bank: Rs. 1,250.00 debited from A/c XX1234 towards ZOMATO.",
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if msg.Status != model.StatusReceived {
		t.Fatalf("status = %q, want RECEIVED", msg.Status)
	}
}

// external_id and content_hash are separate key spaces; a guid must never be
// able to collide with a hash or vice versa.
func TestDedupKeyPrefersExternalID(t *testing.T) {
	withExternal := &model.RawMessage{ExternalID: "guid-1", ContentHash: "hash-1"}
	withoutExternal := &model.RawMessage{ContentHash: "hash-1"}

	if dedupKey(withExternal) == dedupKey(withoutExternal) {
		t.Fatal("a message with an external id must not share a key with one without")
	}
	if dedupKey(withExternal) != dedupKey(&model.RawMessage{ExternalID: "guid-1", ContentHash: "other"}) {
		t.Fatal("external id should be the only thing the key depends on when present")
	}
	if dedupKey(withoutExternal) != dedupKey(&model.RawMessage{ContentHash: "hash-1"}) {
		t.Fatal("content hash should be the fallback key")
	}
}

func TestIngestBatchRejectsOversizedBatch(t *testing.T) {
	inputs := make([]IngestInput, MaxBatchSize+1)
	for i := range inputs {
		inputs[i] = IngestInput{Body: "Rs. 1 debited"}
	}

	_, err := NewMessageService().IngestBatch("user-1", inputs)
	if err == nil {
		t.Fatalf("expected an error for a batch of %d", len(inputs))
	}
	if !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(MaxBatchSize)) {
		t.Fatalf("error %q should name the limit", err)
	}
}

func TestIngestBatchEmptyIsNotAnError(t *testing.T) {
	results, err := NewMessageService().IngestBatch("user-1", nil)
	if err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %d, want 0", len(results))
	}
}

func TestIngestBatchRequiresUserID(t *testing.T) {
	_, err := NewMessageService().IngestBatch("  ", []IngestInput{{Body: "Rs. 1 debited"}})
	if !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestSyncStateRejectsUnknownSource(t *testing.T) {
	_, err := NewMessageService().SyncState("user-1", model.MessageSource("nope"))
	if !apperrors.Is(err, apperrors.ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}
