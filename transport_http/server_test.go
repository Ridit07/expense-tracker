package transport_http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"expense-tracker/config"
	"expense-tracker/services"
)

const testSecret = "test-secret"

func testServer() http.Handler {
	cfg := &config.Config{HTTPPort: "8080", JWTSecret: testSecret, Env: "test"}
	return NewServer(cfg).Handler
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	testServer().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health body = %s", rec.Body.String())
	}
}

func TestProtectedRouteRequiresToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", strings.NewReader(`{}`))
	testServer().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without token", rec.Code)
	}
}

func TestProtectedRouteRejectsBadToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	testServer().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with bad token", rec.Code)
	}
}

func TestProtectedRoutePassesAuthWithValidToken(t *testing.T) {
	token, err := services.NewAuthService(testSecret).GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	testServer().ServeHTTP(rec, req)

	// Auth passes; the request then fails downstream because no DB is wired in
	// this unit test. We only assert it got past the 401 gate.
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("valid token was rejected with 401")
	}
}
