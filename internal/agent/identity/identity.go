// Package identity is the CLIENT-side agent identity + connection library (P6,
// D-177). An augur edge agent boots knowing three out-of-band facts — a
// single-use enrollment token, the hub address, and the hub's agent-CA bundle
// PEM (the pki-agents CA) — and uses this package to: generate a keypair + CSR,
// Enroll for a short-lived client cert, persist it, dial the hub over mTLS, and
// renew the cert before it expires. There are NO server dependencies here and
// NO InsecureSkipVerify anywhere: the hub's server cert is always validated
// against the provided agent-CA pool.
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// defaultRenewFraction is the point in a cert's lifetime (as a fraction between
// NotBefore and NotAfter) at which renewal becomes due. 0.5 = renew at half-life.
const defaultRenewFraction = 0.5

// GenerateKeyAndCSR produces a fresh EC P-256 keypair and a PKCS#10 CSR carrying
// ONLY the common name. The agent deliberately does not assert org or agent-id
// in the CSR — the hub asserts those in the signed cert (the pki-agents role
// forces use_csr_sans=false / use_csr_common_name=false), so anything the agent
// put here would be ignored. Returns PEM for the private key and the CSR.
func GenerateKeyAndCSR(commonName string) (keyPEM, csrPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("identity: generate key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("identity: marshal key: %w", err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: commonName},
	}, key)
	if err != nil {
		return "", "", fmt.Errorf("identity: create csr: %w", err)
	}
	csrPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))

	return keyPEM, csrPEM, nil
}

// Identity is an agent's private key plus its issued leaf certificate, both PEM.
type Identity struct {
	KeyPEM  string
	CertPEM string
}

// TLSCertificate builds the tls.Certificate the agent presents as its client
// cert on the mTLS channel.
func (i Identity) TLSCertificate() (tls.Certificate, error) {
	cert, err := tls.X509KeyPair([]byte(i.CertPEM), []byte(i.KeyPEM))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("identity: build tls cert: %w", err)
	}
	return cert, nil
}

const (
	keyFileName  = "agent-key.pem"
	certFileName = "agent-cert.pem"
	keyFileMode  = 0o600
	certFileMode = 0o644
	dirFileMode  = 0o700
)

// Store persists an agent's key + issued cert to a state directory (typically
// AGENT_STATE_DIR). Writes are atomic (write-temp then rename) and the private
// key is written 0600.
type Store struct {
	dir string
}

// NewStore builds a Store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) keyPath() string  { return filepath.Join(s.dir, keyFileName) }
func (s *Store) certPath() string { return filepath.Join(s.dir, certFileName) }

// SaveIdentity atomically writes the key (0600) and cert to the state dir.
func (s *Store) SaveIdentity(keyPEM, certPEM string) error {
	if err := os.MkdirAll(s.dir, dirFileMode); err != nil {
		return fmt.Errorf("identity: mkdir state dir: %w", err)
	}
	if err := writeAtomic(s.keyPath(), []byte(keyPEM), keyFileMode); err != nil {
		return fmt.Errorf("identity: write key: %w", err)
	}
	if err := writeAtomic(s.certPath(), []byte(certPEM), certFileMode); err != nil {
		return fmt.Errorf("identity: write cert: %w", err)
	}
	return nil
}

// LoadIdentity reads a previously saved key + cert. ok is false (with no error)
// when either file is absent — the caller then enrolls for the first time.
func (s *Store) LoadIdentity() (keyPEM, certPEM string, ok bool) {
	keyBytes, err := os.ReadFile(s.keyPath())
	if err != nil {
		return "", "", false
	}
	certBytes, err := os.ReadFile(s.certPath())
	if err != nil {
		return "", "", false
	}
	return string(keyBytes), string(certBytes), true
}

// writeAtomic writes data to a temp file in the same directory then renames it
// into place, so a reader never observes a partially written file. The temp file
// is created with the target mode so the key is never briefly world-readable.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// NeedsRenewal reports whether now has passed the renewal threshold — NotBefore
// plus renewAt of the cert's total validity window. A renewAt <= 0 defaults to
// 0.5 (half-life). Returns an error if the leaf can't be parsed.
func (i Identity) NeedsRenewal(now time.Time, renewAt float64) (bool, error) {
	block, _ := pem.Decode([]byte(i.CertPEM))
	if block == nil {
		return false, fmt.Errorf("identity: cert is not decodable PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("identity: parse cert: %w", err)
	}
	if renewAt <= 0 {
		renewAt = defaultRenewFraction
	}
	lifetime := cert.NotAfter.Sub(cert.NotBefore)
	threshold := cert.NotBefore.Add(time.Duration(renewAt * float64(lifetime)))
	return now.After(threshold), nil
}
