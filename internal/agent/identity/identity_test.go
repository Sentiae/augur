package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGenerateKeyAndCSR(t *testing.T) {
	const cn = "agent-42"
	keyPEM, csrPEM, err := GenerateKeyAndCSR(cn)
	if err != nil {
		t.Fatalf("GenerateKeyAndCSR: %v", err)
	}

	// Key is a parseable EC private key.
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		t.Fatalf("key PEM not an EC PRIVATE KEY block: %+v", keyBlock)
	}
	if _, err := x509.ParseECPrivateKey(keyBlock.Bytes); err != nil {
		t.Fatalf("parse EC key: %v", err)
	}

	// CSR parses, carries the CN, and has NO URI SANs (org/agent-id are asserted
	// by the hub in the signed cert, never in the CSR).
	csrBlock, _ := pem.Decode([]byte(csrPEM))
	if csrBlock == nil || csrBlock.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("csr PEM not a CERTIFICATE REQUEST block: %+v", csrBlock)
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CSR: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatalf("CSR signature invalid: %v", err)
	}
	if csr.Subject.CommonName != cn {
		t.Fatalf("CSR CN = %q, want %q", csr.Subject.CommonName, cn)
	}
	if len(csr.URIs) != 0 {
		t.Fatalf("CSR must carry no URI SANs, got %v", csr.URIs)
	}
	if len(csr.DNSNames) != 0 || len(csr.IPAddresses) != 0 || len(csr.EmailAddresses) != 0 {
		t.Fatalf("CSR must carry no SANs, got dns=%v ip=%v email=%v", csr.DNSNames, csr.IPAddresses, csr.EmailAddresses)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Empty dir → not ok, no error.
	if _, _, ok := store.LoadIdentity(); ok {
		t.Fatalf("LoadIdentity on empty dir returned ok=true")
	}

	const keyPEM = "KEY-PEM-CONTENT"
	const certPEM = "CERT-PEM-CONTENT"
	if err := store.SaveIdentity(keyPEM, certPEM); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	gotKey, gotCert, ok := store.LoadIdentity()
	if !ok {
		t.Fatalf("LoadIdentity after save returned ok=false")
	}
	if gotKey != keyPEM || gotCert != certPEM {
		t.Fatalf("round-trip mismatch: key=%q cert=%q", gotKey, gotCert)
	}

	// Key file must be 0600 (skip perm assertion on non-POSIX platforms).
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, keyFileName))
		if err != nil {
			t.Fatalf("stat key file: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file perm = %o, want 600", perm)
		}
	}
}

func TestNeedsRenewalBoundary(t *testing.T) {
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(10 * time.Hour) // half-life at +5h
	id := mintIdentity(t, "agent-boundary", notBefore, notAfter)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before half-life", notBefore.Add(4 * time.Hour), false},
		{"just after half-life", notBefore.Add(5*time.Hour + time.Minute), true},
		{"near expiry", notAfter.Add(-time.Minute), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := id.NeedsRenewal(tt.now, 0.5)
			if err != nil {
				t.Fatalf("NeedsRenewal: %v", err)
			}
			if got != tt.want {
				t.Fatalf("NeedsRenewal(%s) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}

	// renewAt <= 0 defaults to 0.5.
	got, err := id.NeedsRenewal(notBefore.Add(6*time.Hour), 0)
	if err != nil {
		t.Fatalf("NeedsRenewal default: %v", err)
	}
	if !got {
		t.Fatalf("NeedsRenewal with renewAt=0 should default to 0.5 and be due at +6h")
	}
}

func TestNeedsRenewalBadCert(t *testing.T) {
	id := Identity{CertPEM: "not a pem"}
	if _, err := id.NeedsRenewal(time.Now(), 0.5); err == nil {
		t.Fatalf("NeedsRenewal on bad cert should error")
	}
}

func TestTLSCertificateAndSubjectCN(t *testing.T) {
	nb := time.Now().Add(-time.Hour)
	na := time.Now().Add(time.Hour)
	id := mintIdentity(t, "agent-tls", nb, na)

	if _, err := id.TLSCertificate(); err != nil {
		t.Fatalf("TLSCertificate: %v", err)
	}
	if cn := subjectCN(id.CertPEM); cn != "agent-tls" {
		t.Fatalf("subjectCN = %q, want agent-tls", cn)
	}
}

func TestPool(t *testing.T) {
	// A real CA PEM is accepted.
	_, caPEM := mintCACert(t)
	if _, err := pool(caPEM); err != nil {
		t.Fatalf("pool(valid CA) errored: %v", err)
	}

	// Garbage / empty is rejected — must never yield an empty (fail-open) pool.
	for _, bad := range []string{"", "not a pem", "-----BEGIN CERTIFICATE-----\nZ\n-----END CERTIFICATE-----"} {
		if _, err := pool(bad); err == nil {
			t.Fatalf("pool(%q) should error", bad)
		}
	}
}

// mintIdentity issues a self-signed leaf with the given validity window and CN,
// returning it as an Identity (cert + matching key PEM).
func mintIdentity(t *testing.T, cn string, notBefore, notAfter time.Time) Identity {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return Identity{KeyPEM: keyPEM, CertPEM: certPEM}
}

// mintCACert issues a self-signed CA cert, returning the key and the cert PEM.
func mintCACert(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-agent-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
