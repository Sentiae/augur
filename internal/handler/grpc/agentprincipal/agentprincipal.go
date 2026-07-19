// Package agentprincipal carries the authenticated agent identity established by
// the agent-plane mTLS listener. The listener (P5, D-177) verifies the agent's
// client cert, cross-checks the enrolled agents row, and stamps a typed
// Principal onto the request ctx via WithPrincipal; agent-plane RPCs derive their
// acting agent + org from it (PrincipalFromContext / AgentIDFromContext /
// OrgIDFromContext) rather than trusting a forgeable request field. Until the
// interceptor runs (the pre-P5 state, or the pre-cert Enroll RPC) the value is
// absent, so callers fail closed.
package agentprincipal

import (
	"context"

	"github.com/google/uuid"
)

// principalCtxKey is the typed context key for the authenticated agent principal
// (root §18 — never a string literal).
type principalCtxKey struct{}

// Principal is the identity the agent-plane mTLS interceptor derives from a
// verified client cert (its SAN URI spiffe://sentiae/<org>/agent/<id>) after
// cross-checking the enrolled agents row. Both fields are cert-derived — never
// read from the request body.
type Principal struct {
	AgentID uuid.UUID
	OrgID   uuid.UUID
}

// WithPrincipal stamps the authenticated agent principal onto ctx. Set by the
// agent-plane mTLS interceptor (P5) AFTER verifying the client cert + agents row.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFromContext reads the authenticated agent principal. ok is false when
// no mTLS principal is present (pre-interceptor state or the pre-cert Enroll RPC),
// so callers fail closed.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// AgentIDFromContext reads the authenticated agent id from the principal. ok is
// false when no mTLS principal is present, so callers fail closed.
func AgentIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return p.AgentID, true
}

// OrgIDFromContext reads the authenticated agent org from the principal. ok is
// false when no mTLS principal is present, so callers fail closed.
func OrgIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	p, ok := PrincipalFromContext(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return p.OrgID, true
}
