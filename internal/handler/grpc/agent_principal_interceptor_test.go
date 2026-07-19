package grpc_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	internalgrpc "github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc"
	"github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc/agentprincipal"
)

// fakeAgentFinder is a test double for the AgentFinder seam the interceptor uses
// to cross-check a presented cert against the enrolled row.
type fakeAgentFinder struct {
	agent *domain.Agent
	err   error
}

func (f fakeAgentFinder) FindAgentByID(context.Context, uuid.UUID) (*domain.Agent, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.agent, nil
}

// makeAgentCert builds a self-signed leaf carrying the agent SPIFFE URI SAN. The
// handshake-level CA verification is out of scope here (P8) — the interceptor
// only reads the leaf's URIs + public key, so a self-signed cert is sufficient to
// drive its logic.
func makeAgentCert(t *testing.T, orgID, agentID uuid.UUID) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	u, err := url.Parse(fmt.Sprintf("spiffe://sentiae/%s/agent/%s", orgID, agentID))
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: agentID.String()},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{u},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

// peerCtx wraps ctx with a gRPC peer carrying the given client cert as TLS state.
func peerCtx(cert *x509.Certificate) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}},
		},
	})
}

const (
	methodRegister = "/augur.v1.AgentPlaneService/RegisterAgent"
	methodEnroll   = "/augur.v1.AgentPlaneService/Enroll"
)

func TestAgentPrincipalInterceptor_Unary(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	cert := makeAgentCert(t, orgID, agentID)
	fp := internalgrpc.SPKIFingerprint(cert)

	activeAgent := &domain.Agent{ID: agentID, OrganizationID: orgID, Status: domain.AgentStatusActive, CertFingerprint: fp}

	tests := []struct {
		name        string
		method      string
		ctx         context.Context
		finder      fakeAgentFinder
		wantCode    codes.Code // OK ⇒ handler ran
		wantPrincip bool
	}{
		{
			name:     "enroll passes with no cert",
			method:   methodEnroll,
			ctx:      context.Background(),
			finder:   fakeAgentFinder{},
			wantCode: codes.OK,
		},
		{
			name:     "data method no cert rejected",
			method:   methodRegister,
			ctx:      context.Background(),
			finder:   fakeAgentFinder{agent: activeAgent},
			wantCode: codes.Unauthenticated,
		},
		{
			name:        "valid cert active matching agent injects principal",
			method:      methodRegister,
			ctx:         peerCtx(cert),
			finder:      fakeAgentFinder{agent: activeAgent},
			wantCode:    codes.OK,
			wantPrincip: true,
		},
		{
			name:     "revoked agent denied",
			method:   methodRegister,
			ctx:      peerCtx(cert),
			finder:   fakeAgentFinder{agent: &domain.Agent{ID: agentID, OrganizationID: orgID, Status: domain.AgentStatusRevoked, CertFingerprint: fp}},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "fingerprint mismatch rejected",
			method:   methodRegister,
			ctx:      peerCtx(cert),
			finder:   fakeAgentFinder{agent: &domain.Agent{ID: agentID, OrganizationID: orgID, Status: domain.AgentStatusActive, CertFingerprint: "deadbeef"}},
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "unknown agent rejected",
			method:   methodRegister,
			ctx:      peerCtx(cert),
			finder:   fakeAgentFinder{err: domain.ErrAgentNotFound},
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "cert org mismatch rejected",
			method:   methodRegister,
			ctx:      peerCtx(cert),
			finder:   fakeAgentFinder{agent: &domain.Agent{ID: agentID, OrganizationID: uuid.New(), Status: domain.AgentStatusActive, CertFingerprint: fp}},
			wantCode: codes.Unauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ic := internalgrpc.NewAgentPrincipalInterceptor(tt.finder)
			var gotCtx context.Context
			handler := func(ctx context.Context, _ any) (any, error) {
				gotCtx = ctx
				return "ok", nil
			}
			_, err := ic.Unary()(tt.ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method}, handler)

			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err=%v)", got, tt.wantCode, err)
			}
			if tt.wantCode != codes.OK {
				if gotCtx != nil {
					t.Fatalf("handler ran on a rejected call")
				}
				return
			}
			if tt.wantPrincip {
				p, ok := agentprincipal.PrincipalFromContext(gotCtx)
				if !ok {
					t.Fatalf("expected principal injected")
				}
				if p.AgentID != agentID || p.OrgID != orgID {
					t.Fatalf("principal = %+v, want agent=%s org=%s", p, agentID, orgID)
				}
			}
		})
	}
}

// TestAgentPrincipalInterceptor_Stream proves the stream path injects the
// principal into the wrapped stream's context.
func TestAgentPrincipalInterceptor_Stream(t *testing.T) {
	orgID := uuid.New()
	agentID := uuid.New()
	cert := makeAgentCert(t, orgID, agentID)
	fp := internalgrpc.SPKIFingerprint(cert)
	finder := fakeAgentFinder{agent: &domain.Agent{ID: agentID, OrganizationID: orgID, Status: domain.AgentStatusActive, CertFingerprint: fp}}

	ic := internalgrpc.NewAgentPrincipalInterceptor(finder)
	var gotOK bool
	handler := func(_ any, ss grpc.ServerStream) error {
		_, gotOK = agentprincipal.PrincipalFromContext(ss.Context())
		return nil
	}
	ss := &fakeServerStream{ctx: peerCtx(cert)}
	if err := ic.Stream()(nil, ss, &grpc.StreamServerInfo{FullMethod: "/augur.v1.AgentPlaneService/MetricsStream"}, handler); err != nil {
		t.Fatalf("stream interceptor err: %v", err)
	}
	if !gotOK {
		t.Fatalf("expected principal in wrapped stream context")
	}

	// No cert ⇒ Unauthenticated, handler never runs.
	ss2 := &fakeServerStream{ctx: context.Background()}
	err := ic.Stream()(nil, ss2, &grpc.StreamServerInfo{FullMethod: "/augur.v1.AgentPlaneService/MetricsStream"}, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("no-cert stream code = %v, want Unauthenticated", status.Code(err))
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

func TestParseAgentURI(t *testing.T) {
	org := uuid.New()
	agent := uuid.New()
	valid := mustURL(t, fmt.Sprintf("spiffe://sentiae/%s/agent/%s", org, agent))

	tests := []struct {
		name    string
		uris    []*url.URL
		wantOK  bool
		wantOrg uuid.UUID
		wantAg  uuid.UUID
	}{
		{"valid", []*url.URL{valid}, true, org, agent},
		{"wrong scheme", []*url.URL{mustURL(t, fmt.Sprintf("https://sentiae/%s/agent/%s", org, agent))}, false, uuid.Nil, uuid.Nil},
		{"wrong host", []*url.URL{mustURL(t, fmt.Sprintf("spiffe://other/%s/agent/%s", org, agent))}, false, uuid.Nil, uuid.Nil},
		{"missing agent segment", []*url.URL{mustURL(t, fmt.Sprintf("spiffe://sentiae/%s/%s", org, agent))}, false, uuid.Nil, uuid.Nil},
		{"wrong middle segment", []*url.URL{mustURL(t, fmt.Sprintf("spiffe://sentiae/%s/probe/%s", org, agent))}, false, uuid.Nil, uuid.Nil},
		{"bad org uuid", []*url.URL{mustURL(t, fmt.Sprintf("spiffe://sentiae/not-a-uuid/agent/%s", agent))}, false, uuid.Nil, uuid.Nil},
		{"bad agent uuid", []*url.URL{mustURL(t, fmt.Sprintf("spiffe://sentiae/%s/agent/not-a-uuid", org))}, false, uuid.Nil, uuid.Nil},
		{"empty", nil, false, uuid.Nil, uuid.Nil},
		{"valid among noise", []*url.URL{mustURL(t, "https://example.com"), valid}, true, org, agent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOrg, gotAg, ok := internalgrpc.ParseAgentURI(tt.uris)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if gotOrg != tt.wantOrg || gotAg != tt.wantAg {
				t.Fatalf("got org=%s agent=%s, want org=%s agent=%s", gotOrg, gotAg, tt.wantOrg, tt.wantAg)
			}
		})
	}
}

func TestSPKIFingerprint(t *testing.T) {
	cert := makeAgentCert(t, uuid.New(), uuid.New())
	got := internalgrpc.SPKIFingerprint(cert)

	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("fingerprint len = %d, want 64", len(got))
	}
	// Stable across calls.
	if internalgrpc.SPKIFingerprint(cert) != got {
		t.Fatalf("fingerprint not stable")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}
