// Package vault holds augur's outbound Vault client. Its sole member today is
// the agent-plane PKI gateway (P3, D-177): augur is the ONLY signer of
// short-lived per-agent X.509 client certs whose SAN URI carries org + agent-id
// (spiffe://sentiae/<org>/agent/<agent>). The cert is minted at enrollment from
// the dedicated Vault pki-agents mount (role augur-agent, policy augur-agent-pki)
// — the org + agent-id are asserted by the HUB via request params, never read
// from the agent's CSR.
package vault

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	pkconfig "github.com/sentiae/platform-kit/config"
)

const (
	// signPath is the Vault pki-agents mount + augur-agent role sign endpoint.
	signPath = "pki-agents/sign/augur-agent"
	// signTimeout bounds each outbound Vault call (CLAUDE.md §27). augur's other
	// outbound clients carry no circuit breaker, so this matches with a timeout.
	signTimeout = 5 * time.Second
)

// AgentSPIFFEURI builds the SPIFFE SAN URI carried in a per-agent cert. The org +
// agent-id live INSIDE the credential (spiffe://sentiae/<org>/agent/<agent>);
// this exact string is what the hub passes to Vault as uri_sans and what the
// agent-plane mTLS listener later verifies against. Pure — unit-tested.
func AgentSPIFFEURI(orgID, agentID uuid.UUID) string {
	return fmt.Sprintf("spiffe://sentiae/%s/agent/%s", orgID, agentID)
}

// PKIClient signs agent CSRs against the Vault pki-agents mount. Concrete struct
// (augur's outbound-client convention — no port layer).
type PKIClient struct {
	vc *pkconfig.VaultClient
}

// NewPKIClient builds the client over the standard VAULT_* / VAULT_SVID_ROLE env
// (pkconfig.NewFromEnv → SVID login as svc/infrastructure-intelligence, policy
// augur-agent-pki, auto-renewed lease). Mirrors delivery's NewTransitSigner.
func NewPKIClient(ctx context.Context) (*PKIClient, error) {
	vc, err := pkconfig.NewFromEnv(ctx)
	if err != nil {
		return nil, fmt.Errorf("pki: build vault client: %w", err)
	}
	return &PKIClient{vc: vc}, nil
}

// SignAgentCSR signs csrPEM and returns the issued leaf (PEM) plus the CA chain
// (PEM) to hand back with it. The org + agent-id are asserted here as request
// params (common_name + uri_sans) — the role forces use_csr_sans=false /
// use_csr_common_name=false so the agent's CSR cannot forge either.
func (c *PKIClient) SignAgentCSR(ctx context.Context, csrPEM string, orgID, agentID uuid.UUID, ttl time.Duration) (certPEM string, caChainPEM string, err error) {
	callCtx, cancel := context.WithTimeout(ctx, signTimeout)
	defer cancel()

	sec, err := c.vc.Raw().Logical().WriteWithContext(callCtx, signPath, buildSignRequest(csrPEM, orgID, agentID, ttl))
	if err != nil {
		return "", "", fmt.Errorf("pki: sign agent csr: %w", err)
	}
	if sec == nil || sec.Data == nil {
		return "", "", fmt.Errorf("pki: sign agent csr returned no data")
	}
	return parseSignResponse(sec.Data)
}

// Close stops the underlying client's lease renewer.
func (c *PKIClient) Close() error {
	if c.vc == nil {
		return nil
	}
	return c.vc.Close()
}

// buildSignRequest is the pure request-map builder for pki-agents/sign/augur-agent.
// Split out so it can be table-tested without a live Vault.
func buildSignRequest(csrPEM string, orgID, agentID uuid.UUID, ttl time.Duration) map[string]any {
	return map[string]any{
		"csr":         csrPEM,
		"common_name": agentID.String(),
		"uri_sans":    AgentSPIFFEURI(orgID, agentID),
		"ttl":         ttl.String(),
		"format":      "pem",
	}
}

// parseSignResponse is the pure response parser for a Vault pki sign result.
// Split out so it can be table-tested without a live Vault.
func parseSignResponse(data map[string]any) (certPEM string, caChainPEM string, err error) {
	cert, ok := data["certificate"].(string)
	if !ok || cert == "" {
		return "", "", fmt.Errorf("pki: sign response missing certificate")
	}
	chain := extractCAChain(data)
	if chain == "" {
		return "", "", fmt.Errorf("pki: sign response missing ca_chain/issuing_ca")
	}
	return cert, chain, nil
}

// extractCAChain prefers the full ca_chain (Vault returns it as a JSON array,
// decoded to []any of PEM strings) and falls back to the single issuing_ca.
func extractCAChain(data map[string]any) string {
	switch v := data["ca_chain"].(type) {
	case []string:
		if joined := joinNonEmpty(toAnySlice(v)); joined != "" {
			return joined
		}
	case []any:
		if joined := joinNonEmpty(v); joined != "" {
			return joined
		}
	}
	if ca, ok := data["issuing_ca"].(string); ok && ca != "" {
		return ca
	}
	return ""
}

func joinNonEmpty(items []any) string {
	parts := make([]string, 0, len(items))
	for _, e := range items {
		if s, ok := e.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
