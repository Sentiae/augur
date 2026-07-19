package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/platform-kit/tenant"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// ErrAgentPlaneDisabled is returned by the enrollment operations that require a
// signer when the agent plane is off (no Vault-PKI client wired). Fail-closed: a
// control that cannot mint certs must refuse rather than nil-panic.
var ErrAgentPlaneDisabled = errors.New("agent plane disabled")

const (
	// defaultTokenTTL bounds an enrollment token when the caller passes zero.
	defaultTokenTTL = time.Hour
	// defaultCertTTL bounds a signed agent cert when the service is built with a
	// non-positive TTL (config default guard).
	defaultCertTTL = 24 * time.Hour
	// defaultMetricsIntervalSec mirrors RegisterAgent's cadence so a freshly
	// enrolled agent knows how often to stream metrics.
	defaultMetricsIntervalSec int32 = 30
	// rawTokenBytes is the entropy of a minted enrollment token (256-bit).
	rawTokenBytes = 32
)

// CertSigner mints a short-lived per-agent X.509 client cert from a CSR. The org
// + agent-id are asserted by the caller (never read from the CSR) and land in the
// cert's SPIFFE SAN URI. Satisfied by *vault.PKIClient; declared here so the
// enrollment usecase depends on the seam, not the concrete Vault client
// (accept-interfaces — enables a fake signer in tests).
type CertSigner interface {
	SignAgentCSR(ctx context.Context, csrPEM string, orgID, agentID uuid.UUID, ttl time.Duration) (certPEM, caChainPEM string, err error)
}

// AgentStore is the persistence seam the enrollment usecase needs. It is the
// consumer-side view of *postgres.AgentRepository (which satisfies it), declared
// here — not in a port package (augur has no port layer by design, plan P2b) —
// so the usecase logic can be unit-tested with an in-memory fake.
type AgentStore interface {
	SaveAgent(ctx context.Context, a *domain.Agent) error
	FindAgentByID(ctx context.Context, id uuid.UUID) (*domain.Agent, error)
	SaveEnrollmentToken(ctx context.Context, t *domain.EnrollmentToken) error
	FindEnrollmentTokenByHash(ctx context.Context, hash string) (*domain.EnrollmentToken, error)
	ConsumeEnrollmentToken(ctx context.Context, tokenID uuid.UUID, now time.Time) error
}

// AgentEnrollmentService drives the agent identity lifecycle (D-177 P4):
// pre-register → enroll (sign cert) → renew → revoke. It holds the agent store,
// the cert signer, and the issued-cert TTL. When signer is nil the agent plane
// is off and the signing operations fail closed (ErrAgentPlaneDisabled).
type AgentEnrollmentService struct {
	store   AgentStore
	signer  CertSigner
	certTTL time.Duration
}

// NewAgentEnrollmentService wires the enrollment usecase. A nil signer marks the
// agent plane disabled; a non-positive certTTL falls back to defaultCertTTL.
func NewAgentEnrollmentService(store AgentStore, signer CertSigner, certTTL time.Duration) *AgentEnrollmentService {
	if certTTL <= 0 {
		certTTL = defaultCertTTL
	}
	return &AgentEnrollmentService{store: store, signer: signer, certTTL: certTTL}
}

// PreRegisterAgentInput describes an operator/ops pre-registration.
type PreRegisterAgentInput struct {
	OrgID            uuid.UUID
	AgentType        string
	Hostname         string
	WorkloadBindings []string
	TokenTTL         time.Duration
}

// PreRegisterAgentOutput carries the new agent id and the raw enrollment token.
// The raw token is returned ONCE (only its hash is stored) and handed to the
// agent out-of-band.
type PreRegisterAgentOutput struct {
	AgentID  uuid.UUID
	RawToken string
}

// PreRegisterAgent creates a pending agent and a single-use enrollment token,
// under the caller's authenticated org ctx (an operator/ops action). Only the
// token hash is persisted; the raw token is returned once.
func (s *AgentEnrollmentService) PreRegisterAgent(ctx context.Context, in PreRegisterAgentInput) (PreRegisterAgentOutput, error) {
	if s.signer == nil {
		return PreRegisterAgentOutput{}, ErrAgentPlaneDisabled
	}
	agent, err := domain.NewAgent(in.OrgID, in.AgentType, in.Hostname, in.WorkloadBindings)
	if err != nil {
		return PreRegisterAgentOutput{}, err
	}

	raw, err := generateRawToken()
	if err != nil {
		return PreRegisterAgentOutput{}, fmt.Errorf("generate enrollment token: %w", err)
	}
	ttl := in.TokenTTL
	if ttl <= 0 {
		ttl = defaultTokenTTL
	}
	token, err := domain.NewEnrollmentToken(agent.ID, agent.OrganizationID, domain.HashEnrollmentToken(raw), ttl)
	if err != nil {
		return PreRegisterAgentOutput{}, err
	}

	if err := s.store.SaveAgent(ctx, agent); err != nil {
		return PreRegisterAgentOutput{}, fmt.Errorf("save agent: %w", err)
	}
	if err := s.store.SaveEnrollmentToken(ctx, token); err != nil {
		return PreRegisterAgentOutput{}, fmt.Errorf("save enrollment token: %w", err)
	}

	// TODO(D-177 P-outbox): emit agent.registered via outbox once augur has one.
	logger.Info("agent pre-registered: agent_id=%s org_id=%s agent_type=%s", agent.ID, agent.OrganizationID, agent.AgentType)
	return PreRegisterAgentOutput{AgentID: agent.ID, RawToken: raw}, nil
}

// EnrollAgentInput carries the one-time token + CSR presented by an enrolling
// agent that has no client cert yet.
type EnrollAgentInput struct {
	RawToken  string
	CSRPEM    string
	AgentType string
	Hostname  string
}

// EnrollAgentOutput carries the signed leaf + CA chain and the agent id.
type EnrollAgentOutput struct {
	CertPEM            string
	CAChainPEM         string
	AgentID            uuid.UUID
	MetricsIntervalSec int32
}

// EnrollAgent exchanges a single-use token + CSR for a signed agent cert. The
// caller is pre-auth (no org), so every DB lookup runs under
// tenant.WithSystemContext. The single-use token is CONSUMED (atomically) BEFORE
// signing so two concurrent enrolls can never both receive a cert; if signing
// fails after consumption the token stays burned (re-enroll needs a fresh token).
// The org + agent-id passed to the signer come from the AGENT ROW, never the
// request — a request that lies about org cannot change the signed identity.
func (s *AgentEnrollmentService) EnrollAgent(ctx context.Context, in EnrollAgentInput) (EnrollAgentOutput, error) {
	if s.signer == nil {
		return EnrollAgentOutput{}, ErrAgentPlaneDisabled
	}
	sysCtx := tenant.WithSystemContext(ctx)
	now := time.Now().UTC()

	// (a) resolve the token by hash.
	token, err := s.store.FindEnrollmentTokenByHash(sysCtx, domain.HashEnrollmentToken(in.RawToken))
	if err != nil {
		return EnrollAgentOutput{}, err
	}
	// (b) reject an expired token before doing any work.
	if token.IsExpired(now) {
		return EnrollAgentOutput{}, domain.ErrEnrollmentTokenExpired
	}
	// (c) load the agent the token belongs to.
	agent, err := s.store.FindAgentByID(sysCtx, token.AgentID)
	if err != nil {
		return EnrollAgentOutput{}, err
	}
	// (d) CONSUME FIRST — the atomic single-use gate before any signing.
	if err := s.store.ConsumeEnrollmentToken(sysCtx, token.ID, now); err != nil {
		return EnrollAgentOutput{}, err
	}
	// (e) sign — org + agent-id come from the AGENT ROW, never the request.
	certPEM, caChainPEM, err := s.signer.SignAgentCSR(ctx, in.CSRPEM, agent.OrganizationID, agent.ID, s.certTTL)
	if err != nil {
		return EnrollAgentOutput{}, fmt.Errorf("sign agent csr: %w", err)
	}
	// (f) fingerprint the issued cert's public key.
	fingerprint, err := certPublicKeyFingerprint(certPEM)
	if err != nil {
		return EnrollAgentOutput{}, fmt.Errorf("fingerprint issued cert: %w", err)
	}
	// (g) activate + persist.
	if err := agent.Activate(fingerprint); err != nil {
		return EnrollAgentOutput{}, err
	}
	if err := s.store.SaveAgent(sysCtx, agent); err != nil {
		return EnrollAgentOutput{}, fmt.Errorf("save enrolled agent: %w", err)
	}

	// TODO(D-177 P-outbox): emit agent.enrolled via outbox once augur has one.
	logger.Info("agent enrolled: agent_id=%s org_id=%s agent_type=%s", agent.ID, agent.OrganizationID, agent.AgentType)
	return EnrollAgentOutput{
		CertPEM:            certPEM,
		CAChainPEM:         caChainPEM,
		AgentID:            agent.ID,
		MetricsIntervalSec: defaultMetricsIntervalSec,
	}, nil
}

// RenewAgentCertInput carries a fresh CSR for the AUTHENTICATED agent (its id is
// established by the mTLS AgentPrincipal, P5 — never read from the request body).
type RenewAgentCertInput struct {
	AgentID uuid.UUID
	CSRPEM  string
}

// RenewAgentCertOutput carries the rotated leaf cert.
type RenewAgentCertOutput struct {
	CertPEM string
}

// RenewAgentCert rotates an active agent's certificate. It refuses a revoked or
// unknown agent (fail-closed) and updates the stored fingerprint since a renew
// may carry a new keypair. The org + agent-id passed to the signer come from the
// agent row.
func (s *AgentEnrollmentService) RenewAgentCert(ctx context.Context, in RenewAgentCertInput) (RenewAgentCertOutput, error) {
	if s.signer == nil {
		return RenewAgentCertOutput{}, ErrAgentPlaneDisabled
	}
	agent, err := s.store.FindAgentByID(ctx, in.AgentID)
	if err != nil {
		return RenewAgentCertOutput{}, err
	}
	if !agent.IsActive() {
		return RenewAgentCertOutput{}, domain.ErrAgentRevoked
	}
	certPEM, _, err := s.signer.SignAgentCSR(ctx, in.CSRPEM, agent.OrganizationID, agent.ID, s.certTTL)
	if err != nil {
		return RenewAgentCertOutput{}, fmt.Errorf("sign agent csr: %w", err)
	}
	fingerprint, err := certPublicKeyFingerprint(certPEM)
	if err != nil {
		return RenewAgentCertOutput{}, fmt.Errorf("fingerprint renewed cert: %w", err)
	}
	// A renew may present a new keypair; overwrite the fingerprint directly
	// rather than via Activate (which rejects a different fingerprint on an
	// already-active agent as a mismatch).
	agent.CertFingerprint = fingerprint
	agent.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveAgent(ctx, agent); err != nil {
		return RenewAgentCertOutput{}, fmt.Errorf("save renewed agent: %w", err)
	}

	logger.Info("agent cert renewed: agent_id=%s org_id=%s", agent.ID, agent.OrganizationID)
	return RenewAgentCertOutput{CertPEM: certPEM}, nil
}

// RevokeAgentInput names the agent to revoke.
type RevokeAgentInput struct {
	AgentID uuid.UUID
}

// RevokeAgent marks an agent revoked under the caller's authenticated org ctx
// (the row is org-scoped so RLS confines the revoke to the caller's org).
func (s *AgentEnrollmentService) RevokeAgent(ctx context.Context, in RevokeAgentInput) error {
	agent, err := s.store.FindAgentByID(ctx, in.AgentID)
	if err != nil {
		return err
	}
	if err := agent.Revoke(); err != nil {
		return err
	}
	if err := s.store.SaveAgent(ctx, agent); err != nil {
		return fmt.Errorf("save revoked agent: %w", err)
	}

	// TODO(D-177 P-outbox): emit agent.revoked via outbox once augur has one.
	logger.Info("agent revoked: agent_id=%s org_id=%s", agent.ID, agent.OrganizationID)
	return nil
}

// generateRawToken mints a 256-bit crypto/rand enrollment token, base64url
// (unpadded) encoded. crypto/rand per root §30.7 (never math/rand for secrets).
func generateRawToken() (string, error) {
	b := make([]byte, rawTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// certPublicKeyFingerprint parses an issued cert PEM and returns the SHA-256 hex
// of its SubjectPublicKeyInfo — a stable identity for the agent's keypair that
// the mTLS listener (P5) can pin.
func certPublicKeyFingerprint(certPEM string) (string, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("no CERTIFICATE PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}
