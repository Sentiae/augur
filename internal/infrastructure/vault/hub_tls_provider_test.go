package vault

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// selfSignedCertPEM returns a valid, parseable X.509 certificate in PEM form for
// the DER/PEM handling tests — no live Vault needed.
func selfSignedCertPEM(t *testing.T) (pemStr string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemStr = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return pemStr, der
}

func TestParseCAResponse_KnownPEM(t *testing.T) {
	want, _ := selfSignedCertPEM(t)

	got, err := parseCAResponse(map[string]any{"certificate": want})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Fatalf("parseCAResponse returned a different PEM than fed")
	}
}

func TestParseCAResponse_Errors(t *testing.T) {
	if _, err := parseCAResponse(map[string]any{}); err == nil {
		t.Error("missing certificate: expected error")
	}
	if _, err := parseCAResponse(map[string]any{"certificate": ""}); err == nil {
		t.Error("empty certificate: expected error")
	}
	if _, err := parseCAResponse(map[string]any{"certificate": "not a cert"}); err == nil {
		t.Error("garbage certificate: expected error")
	}
}

func TestEnsurePEM_DERToPEM(t *testing.T) {
	wantPEM, der := selfSignedCertPEM(t)

	// Feed raw DER (as a string, the defensive branch) and expect valid PEM back
	// that parses to the same certificate.
	got, err := ensurePEM(string(der))
	if err != nil {
		t.Fatalf("ensurePEM(DER): %v", err)
	}
	block, _ := pem.Decode([]byte(got))
	if block == nil {
		t.Fatalf("ensurePEM(DER) did not produce decodable PEM")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		t.Fatalf("ensurePEM(DER) produced unparseable cert: %v", err)
	}
	if strings.Contains(wantPEM, "BEGIN CERTIFICATE") && !strings.Contains(got, "BEGIN CERTIFICATE") {
		t.Fatalf("ensurePEM(DER) output is not PEM-wrapped")
	}
}

func TestServerTLSConfig(t *testing.T) {
	// Construct a provider directly (no live Vault) — same package, so the
	// unexported clientCAs is settable.
	pool := x509.NewCertPool()
	p := &HubTLSProvider{clientCAs: pool}

	cfg := p.ServerTLSConfig()

	if cfg.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Errorf("ClientAuth = %v, want VerifyClientCertIfGiven", cfg.ClientAuth)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %v, want TLS1.3", cfg.MinVersion)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs is nil, want non-nil pool")
	}
	if cfg.GetCertificate == nil {
		t.Error("GetCertificate is nil, want the provider's callback")
	}
}
