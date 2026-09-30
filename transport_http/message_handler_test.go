package transport_http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"expense-tracker/services"
)

// authedRequest builds a request that gets past the JWT gate, so the assertions
// below are about the ingest handler rather than the middleware.
func authedRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()

	token, err := services.NewAuthService(testSecret).GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not a JSON envelope: %s", rec.Body.String())
	}
	if envelope.Error == nil {
		t.Fatalf("response carries no error: %s", rec.Body.String())
	}
	return envelope.Error.Code
}

// An oversized body used to be truncated and then reported as malformed JSON,
// which pointed the client at the wrong problem entirely.
func TestIngestRejectsOversizedBodyAsTooLarge(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	for i := 0; sb.Len() < 6<<20; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"body":"Rs. %d debited from A/c XX1234 towards %s"}`, i, strings.Repeat("X", 512))
	}
	sb.WriteString(`]}`)

	rec := httptest.NewRecorder()
	testServer().ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/v1/messages", sb.String()))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "payload_too_large" {
		t.Fatalf("error code = %q, want payload_too_large", code)
	}
}

func TestIngestRejectsTooManyMessages(t *testing.T) {
	msgs := make([]string, services.MaxBatchSize+1)
	for i := range msgs {
		msgs[i] = `{"body":"Rs. 1 debited"}`
	}
	body := `{"messages":[` + strings.Join(msgs, ",") + `]}`

	rec := httptest.NewRecorder()
	testServer().ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/v1/messages", body))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "batch_too_large" {
		t.Fatalf("error code = %q, want batch_too_large", code)
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprint(services.MaxBatchSize)) {
		t.Fatalf("error should name the %d limit: %s", services.MaxBatchSize, rec.Body.String())
	}
}

// A typo'd source used to be stored verbatim, leaving a value no later stage
// could interpret and nothing to signal the mistake.
func TestIngestRejectsUnknownSource(t *testing.T) {
	body := `{"body":"Rs. 1 debited","source":"macos_chat_db"}`

	rec := httptest.NewRecorder()
	testServer().ServeHTTP(rec, authedRequest(t, http.MethodPost, "/api/v1/messages", body))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_input" {
		t.Fatalf("error code = %q, want invalid_input", code)
	}
	if !strings.Contains(rec.Body.String(), "macos_chatdb") {
		t.Fatalf("error should list the accepted sources: %s", rec.Body.String())
	}
}

func TestSyncStateRejectsUnknownSource(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer().ServeHTTP(rec, authedRequest(t, http.MethodGet, "/api/v1/messages/sync-state?source=chatdb", ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "invalid_input" {
		t.Fatalf("error code = %q, want invalid_input", code)
	}
}

// sync-state shares a path prefix with /messages/{id}; if the wildcard won,
// the sync tool would get a 404 for an id that doesn't exist.
func TestSyncStateRouteBeatsIDWildcard(t *testing.T) {
	mux := http.NewServeMux()
	NewMessageHandler(services.NewMessageService()).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/sync-state", nil)
	_, pattern := mux.Handler(req)

	if pattern != "GET /api/v1/messages/sync-state" {
		t.Fatalf("matched pattern = %q, want the literal sync-state route", pattern)
	}
}
