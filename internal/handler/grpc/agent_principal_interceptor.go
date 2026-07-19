package grpc

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/sentiae/platform-kit/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc/agentprincipal"
)

// certExemptMethods are the agent-plane methods reachable WITHOUT a verified
// client cert. Enroll is pre-cert (the agent has no cert yet; it is token-gated
// inside the usecase). Health + reflection are infra probes. Every other
// agent-plane method requires a cert-derived principal.
var certExemptMethods = map[string]bool{
	"/augur.v1.AgentPlaneService/Enroll":                            true,
	"/grpc.health.v1.Health/Check":                                  true,
	"/grpc.health.v1.Health/Watch":                                  true,
	"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo":     true,
	"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo": true,
}

// AgentPrincipalInterceptor authenticates agent-plane RPCs from the caller's mTLS
// client cert (P5, D-177). The cert was already cryptographically verified against
// the agent CA at the TLS handshake (VerifyClientCertIfGiven), so a PRESENT leaf
// is CA-valid; this interceptor turns that cert into a typed agentprincipal by
// parsing its SAN URI, cross-checking the enrolled agents row (active +
// fingerprint + org), and injecting the principal. It is fail-closed: any
// missing/malformed cert, unknown/revoked agent, or fingerprint mismatch rejects
// before the handler runs.
type AgentPrincipalInterceptor struct {
	agents AgentFinder
}

// NewAgentPrincipalInterceptor builds the interceptor over the agents store used
// to cross-check the presented cert against the enrolled row.
func NewAgentPrincipalInterceptor(agents AgentFinder) *AgentPrincipalInterceptor {
	return &AgentPrincipalInterceptor{agents: agents}
}

// Unary returns the unary server interceptor.
func (i *AgentPrincipalInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		newCtx, err := i.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(newCtx, req)
	}
}

// Stream returns the stream server interceptor. It rewrites the stream's context
// so downstream handlers read the injected principal via stream.Context().
func (i *AgentPrincipalInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		newCtx, err := i.authenticate(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: newCtx})
	}
}

// authenticate is the shared unary+stream logic: exempt the pre-cert/infra
// methods, otherwise require a verified client cert and derive a cross-checked
// principal. Returns the principal-stamped ctx or a fail-closed status error.
func (i *AgentPrincipalInterceptor) authenticate(ctx context.Context, fullMethod string) (context.Context, error) {
	if certExemptMethods[fullMethod] {
		return ctx, nil
	}

	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "client certificate required")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, status.Error(codes.Unauthenticated, "client certificate required")
	}
	leaf := tlsInfo.State.PeerCertificates[0]

	orgID, agentID, ok := ParseAgentURI(leaf.URIs)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid agent certificate identity")
	}

	// The principal is not established yet, so the cross-check read runs under a
	// system ctx (the row is looked up by primary key, not tenant-scoped input).
	agent, err := i.agents.FindAgentByID(tenant.WithSystemContext(ctx), agentID)
	if err != nil {
		if errors.Is(err, domain.ErrAgentNotFound) {
			return nil, status.Error(codes.Unauthenticated, "unknown agent")
		}
		return nil, status.Error(codes.Internal, "agent lookup failed")
	}
	if !agent.IsActive() {
		return nil, status.Error(codes.PermissionDenied, "agent revoked")
	}
	if agent.CertFingerprint != SPKIFingerprint(leaf) {
		// A superseded/stolen cert whose key no longer matches the enrolled row —
		// this is what makes revoke-and-reissue + rotation enforceable.
		return nil, status.Error(codes.Unauthenticated, "certificate does not match enrolled agent")
	}
	if agent.OrganizationID != orgID {
		// Defense in depth: the cert's org SAN must agree with the enrolled row.
		return nil, status.Error(codes.Unauthenticated, "certificate org does not match enrolled agent")
	}

	return agentprincipal.WithPrincipal(ctx, agentprincipal.Principal{AgentID: agentID, OrgID: orgID}), nil
}

// ParseAgentURI extracts the org + agent ids from an agent cert's SAN URIs. It
// accepts the first URI matching spiffe://sentiae/<org_uuid>/agent/<agent_uuid>
// and rejects a wrong scheme/host, a malformed path, or non-UUID segments. Pure —
// table-tested.
func ParseAgentURI(uris []*url.URL) (orgID, agentID uuid.UUID, ok bool) {
	for _, u := range uris {
		if u == nil || u.Scheme != "spiffe" || u.Host != "sentiae" {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 3 || parts[1] != "agent" {
			continue
		}
		oid, err1 := uuid.Parse(parts[0])
		aid, err2 := uuid.Parse(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		return oid, aid, true
	}
	return uuid.Nil, uuid.Nil, false
}

// SPKIFingerprint returns the SHA-256 hex of a cert's SubjectPublicKeyInfo — the
// stable keypair identity pinned at enrollment (domain.Agent.CertFingerprint). It
// must match the value the enrollment usecase computed from the issued cert.
func SPKIFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// wrappedServerStream overrides a grpc.ServerStream's context so the injected
// principal reaches the stream handler (mirrors platform-kit's unexported helper).
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context { return w.ctx }
