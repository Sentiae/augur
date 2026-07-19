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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	pkconfig "github.com/sentiae/platform-kit/config"
)

const (
	// signPath is the Vault pki-agents mount + augur-agent role sign endpoint.
	signPath = "pki-agents/sign/augur-agent"
	// hubIssuePath is the Vault pki-agents mount + augur-hub role issue endpoint
	// (P5a, D-177). Vault generates the hub's server keypair (issue, not sign) —
	// the hub's server key is ephemeral/rotating so CSR management buys nothing.
	hubIssuePath = "pki-agents/issue/augur-hub"
	// caReadPath returns the pki-agents CA public cert (PEM in `certificate`). The
	// hub loads it into its listener's ClientCAs pool to verify agent client certs.
	caReadPath = "pki-agents/cert/ca"
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

// FetchAgentCA reads the pki-agents CA public cert (PEM). This is the trust pool
// the hub's agent-plane mTLS listener verifies AGENT client certs against — the
// same CA that signs those client certs (P3) and the hub's own server cert (P5a).
// Vault's cert/ca returns PEM in `certificate`; ensurePEM defensively converts a
// DER body if a future endpoint ever returns raw DER.
func (c *PKIClient) FetchAgentCA(ctx context.Context) (caPEM string, err error) {
	callCtx, cancel := context.WithTimeout(ctx, signTimeout)
	defer cancel()

	sec, err := c.vc.Raw().Logical().ReadWithContext(callCtx, caReadPath)
	if err != nil {
		return "", fmt.Errorf("pki: read agent ca: %w", err)
	}
	if sec == nil || sec.Data == nil {
		return "", fmt.Errorf("pki: read agent ca returned no data")
	}
	return parseCAResponse(sec.Data)
}

// IssueHubServerCert issues the hub's agent-plane mTLS SERVER cert from the
// pki-agents augur-hub role (P5a, D-177). Vault generates the keypair, so this
// returns the leaf (PEM), its private key (PEM), and the CA chain (PEM). The CN /
// IP SANs / DNS SANs come from config (AgentPlaneConfig.Hub*); the agents that
// dial the hub validate the presented cert against the pki-agents CA, so these
// must match the address agents dial. Identity is passed explicitly to match
// SignAgentCSR's stateless style.
func (c *PKIClient) IssueHubServerCert(ctx context.Context, cn string, ipSANs, dnsSANs []string, ttl time.Duration) (certPEM string, keyPEM string, caChainPEM string, err error) {
	callCtx, cancel := context.WithTimeout(ctx, signTimeout)
	defer cancel()

	sec, err := c.vc.Raw().Logical().WriteWithContext(callCtx, hubIssuePath, buildHubIssueRequest(cn, ipSANs, dnsSANs, ttl))
	if err != nil {
		return "", "", "", fmt.Errorf("pki: issue hub server cert: %w", err)
	}
	if sec == nil || sec.Data == nil {
		return "", "", "", fmt.Errorf("pki: issue hub server cert returned no data")
	}
	return parseIssueResponse(sec.Data)
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

// buildHubIssueRequest is the pure request-map builder for
// pki-agents/issue/augur-hub. Vault accepts ip_sans / alt_names as
// comma-separated strings. Split out so it can be table-tested without a live
// Vault.
func buildHubIssueRequest(cn string, ipSANs, dnsSANs []string, ttl time.Duration) map[string]any {
	return map[string]any{
		"common_name": cn,
		"ip_sans":     strings.Join(ipSANs, ","),
		"alt_names":   strings.Join(dnsSANs, ","),
		"ttl":         ttl.String(),
		"format":      "pem",
	}
}

// parseIssueResponse is the pure response parser for a Vault pki issue result
// (leaf + generated private key + CA chain). Split out for table-testing.
func parseIssueResponse(data map[string]any) (certPEM string, keyPEM string, caChainPEM string, err error) {
	cert, ok := data["certificate"].(string)
	if !ok || cert == "" {
		return "", "", "", fmt.Errorf("pki: issue response missing certificate")
	}
	key, ok := data["private_key"].(string)
	if !ok || key == "" {
		return "", "", "", fmt.Errorf("pki: issue response missing private_key")
	}
	chain := extractCAChain(data)
	if chain == "" {
		return "", "", "", fmt.Errorf("pki: issue response missing ca_chain/issuing_ca")
	}
	return cert, key, chain, nil
}

// parseCAResponse extracts the CA cert (PEM) from a pki-agents/cert/ca read.
func parseCAResponse(data map[string]any) (string, error) {
	cert, ok := data["certificate"].(string)
	if !ok || cert == "" {
		return "", fmt.Errorf("pki: agent ca response missing certificate")
	}
	return ensurePEM(cert)
}

// ensurePEM validates that raw is a parseable X.509 certificate and returns it as
// PEM. Vault's cert/ca returns PEM already; the DER branch is defensive in case a
// raw-DER endpoint is ever substituted.
func ensurePEM(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.Contains(trimmed, "BEGIN CERTIFICATE") {
		block, _ := pem.Decode([]byte(trimmed))
		if block == nil {
			return "", fmt.Errorf("pki: agent ca is not decodable PEM")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return "", fmt.Errorf("pki: agent ca parse: %w", err)
		}
		return trimmed, nil
	}
	der := []byte(raw)
	if _, err := x509.ParseCertificate(der); err != nil {
		return "", fmt.Errorf("pki: agent ca is neither PEM nor DER: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}
