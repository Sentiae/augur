package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHashEnrollmentToken(t *testing.T) {
	h1 := HashEnrollmentToken("secret-token")
	h2 := HashEnrollmentToken("secret-token")
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 { // sha256 hex
		t.Errorf("expected 64 hex chars, got %d", len(h1))
	}
	if HashEnrollmentToken("other") == h1 {
		t.Error("different inputs must hash differently")
	}
}

func TestNewEnrollmentToken(t *testing.T) {
	org := uuid.New()
	agent := uuid.New()
	tok, err := NewEnrollmentToken(agent, org, "hash", time.Hour)
	if err != nil {
		t.Fatalf("NewEnrollmentToken: %v", err)
	}
	if tok.ID == uuid.Nil {
		t.Error("expected minted ID")
	}
	if tok.IsConsumed() {
		t.Error("new token must not be consumed")
	}
	if !tok.ExpiresAt.After(tok.CreatedAt) {
		t.Error("expiry must be after creation")
	}

	if _, err := NewEnrollmentToken(agent, uuid.Nil, "hash", time.Hour); !errors.Is(err, ErrInvalidAgentOrg) {
		t.Errorf("nil org: got %v, want %v", err, ErrInvalidAgentOrg)
	}
}

func TestEnrollmentTokenExpiry(t *testing.T) {
	now := time.Now().UTC()
	tok := &EnrollmentToken{ExpiresAt: now.Add(time.Hour)}
	if tok.IsExpired(now) {
		t.Error("token must not be expired before expiry")
	}
	if !tok.IsExpired(now.Add(2 * time.Hour)) {
		t.Error("token must be expired after expiry")
	}
}

func TestEnrollmentTokenConsume(t *testing.T) {
	now := time.Now().UTC()

	// happy path
	tok := &EnrollmentToken{ExpiresAt: now.Add(time.Hour)}
	if err := tok.Consume(now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !tok.IsConsumed() {
		t.Error("expected consumed")
	}
	// second consume rejected
	if err := tok.Consume(now); !errors.Is(err, ErrEnrollmentTokenConsumed) {
		t.Errorf("double consume: got %v, want %v", err, ErrEnrollmentTokenConsumed)
	}

	// expired token
	expired := &EnrollmentToken{ExpiresAt: now.Add(-time.Hour)}
	if err := expired.Consume(now); !errors.Is(err, ErrEnrollmentTokenExpired) {
		t.Errorf("expired consume: got %v, want %v", err, ErrEnrollmentTokenExpired)
	}
}
