// Package agentprincipal carries the authenticated agent identity established by
// the agent-plane mTLS listener. The listener (P5, D-177) verifies the agent's
// client cert and stamps its agent-id onto the request ctx via WithAgentID; RPCs
// that must act as the CALLING agent (Renew) read it via AgentIDFromContext
// rather than trusting a forgeable request field. Until P5 wires the interceptor
// the value is absent, so those RPCs fail closed (Unauthenticated).
package agentprincipal

import (
	"context"

	"github.com/google/uuid"
)

// agentIDCtxKey is the typed context key for the authenticated agent id (root
// §18 — never a string literal).
type agentIDCtxKey struct{}

// WithAgentID stamps the authenticated agent id onto ctx. Set by the agent-plane
// mTLS interceptor (P5) AFTER verifying the client cert.
func WithAgentID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, agentIDCtxKey{}, id)
}

// AgentIDFromContext reads the authenticated agent id. ok is false when no mTLS
// principal is present (the pre-P5 state), so callers fail closed.
func AgentIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(agentIDCtxKey{}).(uuid.UUID)
	return v, ok
}
