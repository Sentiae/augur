package domain

import "errors"

var (
	// Agent errors
	ErrAgentNotFound            = errors.New("agent not found")
	ErrInvalidAgentOrg          = errors.New("invalid agent organization")
	ErrInvalidAgentType         = errors.New("invalid agent type")
	ErrAgentFingerprintMismatch = errors.New("agent certificate fingerprint mismatch")
	ErrAgentRevoked             = errors.New("agent is revoked")

	// Enrollment token errors
	ErrEnrollmentTokenNotFound = errors.New("enrollment token not found")
	ErrEnrollmentTokenExpired  = errors.New("enrollment token expired")
	ErrEnrollmentTokenConsumed = errors.New("enrollment token already consumed")
)
