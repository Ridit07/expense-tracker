package services

import "testing"

func TestTokenRoundTrip(t *testing.T) {
	svc := NewAuthService("secret")

	token, err := svc.GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	userID, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if userID != "user-123" {
		t.Fatalf("subject = %q, want user-123", userID)
	}
}

func TestParseTokenRejectsWrongSecret(t *testing.T) {
	token, err := NewAuthService("secret-a").GenerateToken("user-123")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	if _, err := NewAuthService("secret-b").ParseToken(token); err == nil {
		t.Fatal("expected error for token signed with a different secret")
	}
}

func TestParseTokenRejectsGarbage(t *testing.T) {
	if _, err := NewAuthService("secret").ParseToken("garbage"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}
