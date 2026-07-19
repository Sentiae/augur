package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
)

// pool builds an x509 cert pool from a PEM bundle, failing loudly if nothing
// parses (a bad/empty CA PEM must never silently produce an empty trust pool —
// that would be a fail-open handshake).
func pool(caPEM string) (*x509.CertPool, error) {
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, fmt.Errorf("identity: no valid certificate in CA PEM bundle")
	}
	return cp, nil
}

// Enroll performs the pre-cert bootstrap: dial the hub over SERVER-auth TLS
// (validating the hub's cert against the out-of-band agent-CA bundle — the hub's
// VerifyClientCertIfGiven listener accepts a client with no cert here, gating on
// the token instead), generate a keypair + CSR, and exchange them for a signed
// client cert. The returned Identity is durable; caABundle-preferring keeps the
// hub-provided CA as the RootCAs for all subsequent mTLS dials.
func Enroll(ctx context.Context, hubAddr, token, agentType, hostname, hubCAPEM string) (id Identity, agentID string, metricsInterval int32, err error) {
	cp, err := pool(hubCAPEM)
	if err != nil {
		return Identity{}, "", 0, err
	}
	creds := credentials.NewTLS(&tls.Config{
		RootCAs:    cp,
		MinVersion: tls.VersionTLS13,
	})
	conn, err := grpc.NewClient(hubAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return Identity{}, "", 0, fmt.Errorf("identity: dial hub for enroll: %w", err)
	}
	defer conn.Close()

	keyPEM, csrPEM, err := GenerateKeyAndCSR(hostname)
	if err != nil {
		return Identity{}, "", 0, err
	}

	client := augurv1.NewAgentPlaneServiceClient(conn)
	resp, err := client.Enroll(ctx, &augurv1.EnrollRequest{
		JoinToken: token,
		CsrPem:    csrPEM,
		AgentType: agentType,
		Hostname:  hostname,
	})
	if err != nil {
		return Identity{}, "", 0, fmt.Errorf("identity: enroll rpc: %w", err)
	}
	if resp.GetCertPem() == "" {
		return Identity{}, "", 0, fmt.Errorf("identity: enroll response missing cert")
	}
	return Identity{KeyPEM: keyPEM, CertPEM: resp.GetCertPem()}, resp.GetAgentId(), resp.GetMetricsIntervalSec(), nil
}

// Renew rotates the agent's cert over the EXISTING mTLS channel. A fresh keypair
// is minted per renewal (cheaper than proving possession of the old key and it
// bounds key exposure). The hub authorizes the rotation from the presented
// client cert on the channel, not from the CSR; current's CN is reused only so
// the CSR subject stays stable (the hub ignores it via use_csr_common_name=false).
func Renew(ctx context.Context, client augurv1.AgentPlaneServiceClient, current Identity) (Identity, error) {
	cn := subjectCN(current.CertPEM)
	keyPEM, csrPEM, err := GenerateKeyAndCSR(cn)
	if err != nil {
		return Identity{}, err
	}
	resp, err := client.Renew(ctx, &augurv1.RenewRequest{CsrPem: csrPEM})
	if err != nil {
		return Identity{}, fmt.Errorf("identity: renew rpc: %w", err)
	}
	if resp.GetCertPem() == "" {
		return Identity{}, fmt.Errorf("identity: renew response missing cert")
	}
	return Identity{KeyPEM: keyPEM, CertPEM: resp.GetCertPem()}, nil
}

// subjectCN extracts the leaf's subject common name, or "" if the cert can't be
// parsed. Only used to keep the renewal CSR subject stable; the hub ignores it.
func subjectCN(certPEM string) string {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return cert.Subject.CommonName
}
