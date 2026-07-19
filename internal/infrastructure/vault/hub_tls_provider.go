package vault

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// HubTLSConfig is the hub server-cert identity the provider requests at boot
// (P5a, D-177). Kept local to the vault package so this infrastructure client
// does not depend on pkg/config; P5b maps AgentPlaneConfig → HubTLSConfig when it
// wires the provider into the DI container.
type HubTLSConfig struct {
	CommonName string
	IPSANs     []string
	DNSSANs    []string
	CertTTL    time.Duration
}

// HubTLSProvider supplies the TLS material the hub's agent-plane mTLS listener
// needs (P5a, D-177): its OWN server cert (issued from the pki-agents augur-hub
// role) and the agent-CA pool used to verify AGENT client certs. It fails closed
// — construction returns an error if either the server cert or the CA cannot be
// obtained, so no listener ever starts without valid material — and it keeps the
// server cert fresh via a renewal goroutine that re-issues at ~half TTL and
// atomically swaps the stored cert so rotation is seamless for callers using
// tls.Config.GetCertificate.
//
// NOTE: P5b wires this into the DI container + agent listener + shutdown. Today
// it is constructed only in tests.
type HubTLSProvider struct {
	pki *PKIClient
	cfg HubTLSConfig

	mu   sync.RWMutex
	cert *tls.Certificate

	// clientCAs is immutable after construction (the agent CA does not rotate
	// within a hub lifetime) → no lock needed to read it.
	clientCAs *x509.CertPool

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewHubTLSProvider issues the hub server cert + fetches the agent CA, builds the
// serving cert and the ClientCAs pool, and starts the renewal goroutine bound to
// ctx. Returns an error (fail-closed) if either the cert or the CA is missing.
func NewHubTLSProvider(ctx context.Context, pki *PKIClient, cfg HubTLSConfig) (*HubTLSProvider, error) {
	if pki == nil {
		return nil, fmt.Errorf("hub tls: nil PKIClient")
	}
	p := &HubTLSProvider{pki: pki, cfg: cfg}

	if err := p.issueAndStore(ctx); err != nil {
		return nil, fmt.Errorf("hub tls: initial server cert issue: %w", err)
	}

	caPEM, err := pki.FetchAgentCA(ctx)
	if err != nil {
		return nil, fmt.Errorf("hub tls: fetch agent ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, fmt.Errorf("hub tls: agent ca PEM could not be appended to the client-CA pool")
	}
	p.clientCAs = pool

	renewCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.wg.Add(1)
	go p.renewLoop(renewCtx)

	return p, nil
}

// GetCertificate returns the CURRENT server cert under a read lock, for
// tls.Config.GetCertificate — so a rotation mid-flight is seamless.
func (p *HubTLSProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cert == nil {
		return nil, fmt.Errorf("hub tls: no server certificate available")
	}
	return p.cert, nil
}

// ClientCAs returns the pool the listener verifies agent client certs against.
func (p *HubTLSProvider) ClientCAs() *x509.CertPool { return p.clientCAs }

// ServerTLSConfig is exactly what P5b's agent-plane listener uses.
// VerifyClientCertIfGiven (not RequireAndVerifyClientCert) lets the pre-cert
// Enroll RPC connect with no client cert; per-method enforcement is P5b's
// interceptor. TLS 1.3 minimum; agent client certs verified against ClientCAs.
func (p *HubTLSProvider) ServerTLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: p.GetCertificate,
		ClientCAs:      p.ClientCAs(),
		ClientAuth:     tls.VerifyClientCertIfGiven,
		MinVersion:     tls.VersionTLS13,
	}
}

// Stop cancels the renewal goroutine and waits for it to exit. Idempotent-safe
// for a single Stop; P5b wires it into graceful shutdown.
func (p *HubTLSProvider) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

// issueAndStore issues a fresh hub server cert and atomically swaps it in.
func (p *HubTLSProvider) issueAndStore(ctx context.Context) error {
	certPEM, keyPEM, chainPEM, err := p.pki.IssueHubServerCert(ctx, p.cfg.CommonName, p.cfg.IPSANs, p.cfg.DNSSANs, p.cfg.CertTTL)
	if err != nil {
		return err
	}
	// Serve leaf + CA chain so a client can build the path to the trusted root.
	fullChain := certPEM
	if chainPEM != "" {
		fullChain = certPEM + "\n" + chainPEM
	}
	tlsCert, err := tls.X509KeyPair([]byte(fullChain), []byte(keyPEM))
	if err != nil {
		return fmt.Errorf("hub tls: build keypair: %w", err)
	}
	// Parse the leaf so the renewal loop can schedule against its NotAfter.
	if leaf, perr := x509.ParseCertificate(tlsCert.Certificate[0]); perr == nil {
		tlsCert.Leaf = leaf
	}

	p.mu.Lock()
	p.cert = &tlsCert
	p.mu.Unlock()
	return nil
}

// renewLoop re-issues the server cert at ~half its TTL. Context-aware (§9) with a
// defer recover() (§30.4). On transient failure it retries with backoff and keeps
// serving the old cert until it would expire (then logs loudly).
func (p *HubTLSProvider) renewLoop(ctx context.Context) {
	defer p.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("hub tls: renewal goroutine panic: %v", r)
		}
	}()

	for {
		p.mu.RLock()
		var notAfter time.Time
		if p.cert != nil && p.cert.Leaf != nil {
			notAfter = p.cert.Leaf.NotAfter
		}
		p.mu.RUnlock()

		timer := time.NewTimer(p.renewInterval(notAfter))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := p.renewWithRetry(ctx, notAfter); err != nil {
			// ctx cancelled during retry — shutting down.
			return
		}
	}
}

// renewInterval schedules the next re-issue for when ~half the cert TTL remains.
func (p *HubTLSProvider) renewInterval(notAfter time.Time) time.Duration {
	half := p.cfg.CertTTL / 2
	if half <= 0 {
		// Defensive: a zero/negative TTL must never spin the loop.
		half = 30 * time.Minute
	}
	if notAfter.IsZero() {
		return half
	}
	wait := time.Until(notAfter) - half
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}

// renewWithRetry re-issues with exponential backoff, serving the old cert until
// it would expire. Returns non-nil only when ctx is cancelled.
func (p *HubTLSProvider) renewWithRetry(ctx context.Context, notAfter time.Time) error {
	const maxBackoff = 5 * time.Minute
	backoff := 5 * time.Second

	for {
		if err := p.issueAndStore(ctx); err == nil {
			logger.Info("hub tls: server cert rotated (cn=%s ttl=%s)", p.cfg.CommonName, p.cfg.CertTTL)
			return nil
		} else if !notAfter.IsZero() && time.Now().After(notAfter) {
			logger.Error("hub tls: server cert EXPIRED and renewal still failing: %v", err)
		} else {
			logger.Warn("hub tls: server cert renewal failed, retrying in %s (old cert still valid): %v", backoff, err)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
