package services

import (
	"testing"
	"time"
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
