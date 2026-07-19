package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
)

// EnrollmentToken is a single-use, TTL-bounded secret an agent presents once to
// enroll. Only its hash is stored; the raw token is minted elsewhere (P4) and
// never persisted.
type EnrollmentToken struct {
	ID             uuid.UUID  `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	AgentID        uuid.UUID  `json:"agent_id" gorm:"type:uuid;index"`
	OrganizationID uuid.UUID  `json:"organization_id" gorm:"type:uuid;not null;index"`
	TokenHash      string     `json:"token_hash" gorm:"uniqueIndex"`
	ExpiresAt      time.Time  `json:"expires_at" gorm:"not null"`
	ConsumedAt     *time.Time `json:"consumed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (EnrollmentToken) TableName() string {
	return "augur_agent_enrollment_tokens"
}

// HashEnrollmentToken derives the stored hash of a raw enrollment token.
// A plain SHA-256 (not bcrypt) is correct here: the raw token is a
// high-entropy random secret, so a fast, deterministic hash gives lookup by
// hash with no brute-force exposure.
func HashEnrollmentToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewEnrollmentToken constructs an unconsumed token that expires ttl from now.
func NewEnrollmentToken(agentID, orgID uuid.UUID, tokenHash string, ttl time.Duration) (*EnrollmentToken, error) {
	if orgID == uuid.Nil {
		return nil, ErrInvalidAgentOrg
	}
	now := time.Now().UTC()
	return &EnrollmentToken{
		ID:             uuid.New(),
		AgentID:        agentID,
		OrganizationID: orgID,
		TokenHash:      tokenHash,
		ExpiresAt:      now.Add(ttl),
		CreatedAt:      now,
	}, nil
}

// IsExpired reports whether the token is past its expiry at the given instant.
func (t *EnrollmentToken) IsExpired(now time.Time) bool {
	return now.After(t.ExpiresAt)
}

// IsConsumed reports whether the token has already been used.
func (t *EnrollmentToken) IsConsumed() bool {
	return t.ConsumedAt != nil
}

// Consume marks the token used. It fails if the token was already consumed or
// has expired.
func (t *EnrollmentToken) Consume(now time.Time) error {
	if t.IsConsumed() {
		return ErrEnrollmentTokenConsumed
	}
	if t.IsExpired(now) {
		return ErrEnrollmentTokenExpired
	}
	consumed := now.UTC()
	t.ConsumedAt = &consumed
	return nil
}
