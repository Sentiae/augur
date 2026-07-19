package usecase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// --- fakes -----------------------------------------------------------------

// fakeStore is an in-memory AgentStore. ConsumeEnrollmentToken replicates the
// SQL single-use gate (WHERE consumed_at IS NULL) so the usecase's
// consume-before-sign ordering is exercised without a database. augur has no
// port layer and no Postgres integration harness (plan P2b); the usecase logic
// is proven here with fakes, matching augur's existing stub-based usecase tests.
type fakeStore struct {
	mu     sync.Mutex
	agents map[uuid.UUID]*domain.Agent
	tokens map[uuid.UUID]*domain.EnrollmentToken
	byHash map[string]uuid.UUID
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		agents: map[uuid.UUID]*domain.Agent{},
		tokens: map[uuid.UUID]*domain.EnrollmentToken{},
		byHash: map[string]uuid.UUID{},
	}
}

func (s *fakeStore) SaveAgent(_ context.Context, a *domain.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *a
	s.agents[a.ID] = &cp
	return nil
}

func (s *fakeStore) FindAgentByID(_ context.Context, id uuid.UUID) (*domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		return nil, domain.ErrAgentNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *fakeStore) SaveEnrollmentToken(_ context.Context, t *domain.EnrollmentToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *t
	s.tokens[t.ID] = &cp
	s.byHash[t.TokenHash] = t.ID
	return nil
}

func (s *fakeStore) FindEnrollmentTokenByHash(_ context.Context, hash string) (*domain.EnrollmentToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byHash[hash]
	if !ok {
		return nil, domain.ErrEnrollmentTokenNotFound
	}
	cp := *s.tokens[id]
	return &cp, nil
}

func (s *fakeStore) ConsumeEnrollmentToken(_ context.Context, tokenID uuid.UUID, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[tokenID]
	if !ok || t.ConsumedAt != nil {
		return domain.ErrEnrollmentTokenConsumed
	}
	c := now.UTC()
	t.ConsumedAt = &c
	return nil
}

// fakeSigner records the org + agent-id it was asked to sign for and returns a
// canned self-signed cert so certPublicKeyFingerprint can parse it. calls counts
// invocations so a test can prove NO second cert was minted.
type fakeSigner struct {
	mu       sync.Mutex
	calls    int
	gotOrg   uuid.UUID
	gotAgent uuid.UUID
	certPEM  string
	caPEM    string
	err      error
}

func (f *fakeSigner) SignAgentCSR(_ context.Context, _ string, orgID, agentID uuid.UUID, _ time.Duration) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotOrg = orgID
	f.gotAgent = agentID
	if f.err != nil {
		return "", "", f.err
	}
	return f.certPEM, f.caPEM, nil
}

// selfSignedCertPEM mints a throwaway ECDSA self-signed cert PEM so the
// fingerprint parse in the usecase has a real certificate to work with.
func selfSignedCertPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-agent"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func newSvc(store AgentStore, signer CertSigner) *AgentEnrollmentService {
	return NewAgentEnrollmentService(store, signer, time.Hour)
}

// --- tests -----------------------------------------------------------------

// TestEnroll_HappyPath — pre-register then enroll: agent goes active, the
// fingerprint is set to the SHA-256 of the issued cert's SubjectPublicKeyInfo,
// and the cert + chain are returned.
func TestEnroll_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	cert := selfSignedCertPEM(t)
	signer := &fakeSigner{certPEM: cert, caPEM: "CA-CHAIN"}
	svc := newSvc(store, signer)
	orgA := uuid.New()

	pre, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: orgA, AgentType: "vm", Hostname: "h1"})
	if err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	if pre.RawToken == "" {
		t.Fatal("expected a raw token")
	}

	out, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if out.CertPEM != cert || out.CAChainPEM != "CA-CHAIN" {
		t.Fatalf("cert/chain not returned as issued")
	}
	if out.AgentID != pre.AgentID {
		t.Fatalf("agent id mismatch: %s vs %s", out.AgentID, pre.AgentID)
	}

	got, _ := store.FindAgentByID(ctx, pre.AgentID)
	if !got.IsActive() {
		t.Fatalf("agent not active after enroll: %s", got.Status)
	}
	wantFP, _ := certPublicKeyFingerprint(cert)
	if got.CertFingerprint != wantFP || wantFP == "" {
		t.Fatalf("fingerprint mismatch: got %q want %q", got.CertFingerprint, wantFP)
	}
}

// TestEnroll_TokenSingleUse — a second enroll with the same raw token is rejected
// (ErrEnrollmentTokenConsumed) AND the signer is called exactly once, proving the
// consume-before-sign gate stops a second cert being minted.
func TestEnroll_TokenSingleUse(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	signer := &fakeSigner{certPEM: selfSignedCertPEM(t), caPEM: "CA"}
	svc := newSvc(store, signer)

	pre, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: uuid.New(), AgentType: "vm"})
	if err != nil {
		t.Fatalf("pre-register: %v", err)
	}

	if _, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"}); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	_, err = svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"})
	if !errors.Is(err, domain.ErrEnrollmentTokenConsumed) {
		t.Fatalf("second enroll: got %v, want ErrEnrollmentTokenConsumed", err)
	}
	if signer.calls != 1 {
		t.Fatalf("signer called %d times, want exactly 1 (no second cert)", signer.calls)
	}
}

// TestEnroll_ExpiredToken — an expired token is rejected before any signing.
func TestEnroll_ExpiredToken(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	signer := &fakeSigner{certPEM: selfSignedCertPEM(t), caPEM: "CA"}
	svc := newSvc(store, signer)

	pre, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: uuid.New(), AgentType: "vm"})
	if err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	// Backdate the stored token so it is expired at enroll time.
	store.mu.Lock()
	for _, tk := range store.tokens {
		tk.ExpiresAt = time.Now().Add(-time.Hour)
	}
	store.mu.Unlock()

	_, err = svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"})
	if !errors.Is(err, domain.ErrEnrollmentTokenExpired) {
		t.Fatalf("got %v, want ErrEnrollmentTokenExpired", err)
	}
	if signer.calls != 0 {
		t.Fatalf("signer called %d times on expired token, want 0", signer.calls)
	}
}

// TestEnroll_OrgComesFromAgentRow — the signer receives the AGENT ROW's org +
// agent-id, never a request value. EnrollAgentInput carries no org field, so this
// asserts the signed identity is bound to the pre-registered org structurally.
func TestEnroll_OrgComesFromAgentRow(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	signer := &fakeSigner{certPEM: selfSignedCertPEM(t), caPEM: "CA"}
	svc := newSvc(store, signer)
	orgA := uuid.New()

	pre, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: orgA, AgentType: "vm"})
	if err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	if _, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if signer.gotOrg != orgA {
		t.Fatalf("signer org: got %s, want agent-row org %s", signer.gotOrg, orgA)
	}
	if signer.gotAgent != pre.AgentID {
		t.Fatalf("signer agent: got %s, want %s", signer.gotAgent, pre.AgentID)
	}
}

// TestRenew_AfterRevoke — revoking an agent makes it inactive, so a later renew
// is refused with ErrAgentRevoked.
func TestRenew_AfterRevoke(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	signer := &fakeSigner{certPEM: selfSignedCertPEM(t), caPEM: "CA"}
	svc := newSvc(store, signer)

	pre, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: uuid.New(), AgentType: "vm"})
	if err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	if _, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"}); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	if err := svc.RevokeAgent(ctx, RevokeAgentInput{AgentID: pre.AgentID}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, _ := store.FindAgentByID(ctx, pre.AgentID)
	if got.IsActive() {
		t.Fatal("agent still active after revoke")
	}

	_, err = svc.RenewAgentCert(ctx, RenewAgentCertInput{AgentID: pre.AgentID, CSRPEM: "csr2"})
	if !errors.Is(err, domain.ErrAgentRevoked) {
		t.Fatalf("renew after revoke: got %v, want ErrAgentRevoked", err)
	}
}

// TestRenew_HappyPath — an active agent renews and its fingerprint updates to the
// new cert's key (a renew may carry a new keypair).
func TestRenew_HappyPath(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	cert1 := selfSignedCertPEM(t)
	signer := &fakeSigner{certPEM: cert1, caPEM: "CA"}
	svc := newSvc(store, signer)

	pre, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: uuid.New(), AgentType: "vm"})
	if err != nil {
		t.Fatalf("pre-register: %v", err)
	}
	if _, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: pre.RawToken, CSRPEM: "csr"}); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	// Renew with a DIFFERENT cert (new keypair) — fingerprint must update.
	cert2 := selfSignedCertPEM(t)
	signer.certPEM = cert2
	out, err := svc.RenewAgentCert(ctx, RenewAgentCertInput{AgentID: pre.AgentID, CSRPEM: "csr2"})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if out.CertPEM != cert2 {
		t.Fatal("renew did not return the new cert")
	}
	got, _ := store.FindAgentByID(ctx, pre.AgentID)
	wantFP, _ := certPublicKeyFingerprint(cert2)
	if got.CertFingerprint != wantFP {
		t.Fatalf("fingerprint not updated on renew: got %q want %q", got.CertFingerprint, wantFP)
	}
}

// TestEnroll_UnknownToken — a token that was never issued is rejected.
func TestEnroll_UnknownToken(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(newFakeStore(), &fakeSigner{certPEM: selfSignedCertPEM(t)})
	_, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: "nope", CSRPEM: "csr"})
	if !errors.Is(err, domain.ErrEnrollmentTokenNotFound) {
		t.Fatalf("got %v, want ErrEnrollmentTokenNotFound", err)
	}
}

// TestOps_PlaneDisabled — with a nil signer the signing operations fail closed
// with ErrAgentPlaneDisabled rather than nil-panic.
func TestOps_PlaneDisabled(t *testing.T) {
	ctx := context.Background()
	svc := NewAgentEnrollmentService(newFakeStore(), nil, time.Hour)

	if _, err := svc.PreRegisterAgent(ctx, PreRegisterAgentInput{OrgID: uuid.New(), AgentType: "vm"}); !errors.Is(err, ErrAgentPlaneDisabled) {
		t.Fatalf("pre-register: got %v, want ErrAgentPlaneDisabled", err)
	}
	if _, err := svc.EnrollAgent(ctx, EnrollAgentInput{RawToken: "x", CSRPEM: "csr"}); !errors.Is(err, ErrAgentPlaneDisabled) {
		t.Fatalf("enroll: got %v, want ErrAgentPlaneDisabled", err)
	}
	if _, err := svc.RenewAgentCert(ctx, RenewAgentCertInput{AgentID: uuid.New(), CSRPEM: "csr"}); !errors.Is(err, ErrAgentPlaneDisabled) {
		t.Fatalf("renew: got %v, want ErrAgentPlaneDisabled", err)
	}
}
