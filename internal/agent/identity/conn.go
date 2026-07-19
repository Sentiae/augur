package identity

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

const (
	// reconnectBackoffInitial / Max bound the exponential backoff between
	// connection attempts after a stream/dial failure.
	reconnectBackoffInitial = 1 * time.Second
	reconnectBackoffMax     = 30 * time.Second
	// renewalCheckInterval is how often the serve loop re-evaluates NeedsRenewal.
	renewalCheckInterval = 30 * time.Second
)

// errRenewed signals the serve loop exited cleanly to reconnect under a freshly
// renewed cert (not a failure — skip the backoff penalty).
var errRenewed = errors.New("identity: cert renewed, reconnecting")

// DialHubMTLS opens an mTLS channel to the hub: the agent presents its issued
// client cert and validates the hub's server cert against the agent-CA bundle.
// No InsecureSkipVerify; TLS 1.3 floor.
func DialHubMTLS(_ context.Context, hubAddr string, id Identity, hubCAPEM string) (*grpc.ClientConn, error) {
	cert, err := id.TLSCertificate()
	if err != nil {
		return nil, err
	}
	cp, err := pool(hubCAPEM)
	if err != nil {
		return nil, err
	}
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      cp,
		MinVersion:   tls.VersionTLS13,
	})
	conn, err := grpc.NewClient(hubAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("identity: dial hub mtls: %w", err)
	}
	return conn, nil
}

// RunnerConfig is the per-binary wiring for the shared agent runner. The three
// agent binaries differ only by AgentType and the two callbacks.
type RunnerConfig struct {
	HubAddr            string
	HubCAPEM           string
	EnrollToken        string
	AgentType          string
	Hostname           string
	WorkloadIDs        []string
	StateDir           string
	DefaultIntervalSec int
	RenewAt            float64

	// Collect produces the metrics reports to send this tick (the binary decides
	// how many, per its own workload model).
	Collect func(ctx context.Context, agentID string, workloadIDs []string) []*augurv1.AgentMetricsReport
	// ExecuteScaling handles one scaling command and returns the outcome to report
	// back, or nil to report nothing (e.g. a dry run).
	ExecuteScaling func(ctx context.Context, cmd *augurv1.ScalingCommand) *augurv1.ScalingOutcomeReport
}

// Agent is the shared runner: it owns identity bootstrap (enroll-or-load), the
// mTLS connection lifecycle (dial, register, metrics stream), reconnect with
// backoff, and cert renewal.
type Agent struct {
	cfg     RunnerConfig
	store   *Store
	id      Identity
	agentID string
}

// NewAgent builds a runner from cfg.
func NewAgent(cfg RunnerConfig) *Agent {
	return &Agent{cfg: cfg, store: NewStore(cfg.StateDir)}
}

// Run bootstraps identity then serves until ctx is cancelled, reconnecting with
// backoff across transient failures and renewing the cert in-band.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.ensureIdentity(ctx); err != nil {
		return err
	}
	logger.Info("Agent identity ready: id=%s type=%s", a.agentID, a.cfg.AgentType)

	backoff := reconnectBackoffInitial
	for ctx.Err() == nil {
		err := a.serveConnection(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errRenewed) {
			backoff = reconnectBackoffInitial
			continue
		}
		logger.Error("Connection lost, reconnecting in %s: %v", backoff, err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectBackoffMax {
			backoff = reconnectBackoffMax
		}
	}
	return nil
}

// ensureIdentity loads a persisted identity or, if none, enrolls for one. The
// agent-id is derived from the leaf's subject CN (the hub sets CN = agent-id),
// so it survives a reboot without a stored id and matches the server-assigned id.
func (a *Agent) ensureIdentity(ctx context.Context) error {
	if keyPEM, certPEM, ok := a.store.LoadIdentity(); ok {
		a.id = Identity{KeyPEM: keyPEM, CertPEM: certPEM}
		a.agentID = subjectCN(a.id.CertPEM)
		return nil
	}
	if a.cfg.EnrollToken == "" {
		return fmt.Errorf("identity: no stored identity and AGENT_ENROLL_TOKEN is unset")
	}
	id, agentID, _, err := Enroll(ctx, a.cfg.HubAddr, a.cfg.EnrollToken, a.cfg.AgentType, a.cfg.Hostname, a.cfg.HubCAPEM)
	if err != nil {
		return err
	}
	if err := a.store.SaveIdentity(id.KeyPEM, id.CertPEM); err != nil {
		return err
	}
	a.id = id
	a.agentID = agentID
	if a.agentID == "" {
		a.agentID = subjectCN(id.CertPEM)
	}
	return nil
}

// serveConnection dials, registers, streams and serves one connection until it
// fails, ctx is cancelled, or a renewal forces a clean reconnect.
func (a *Agent) serveConnection(ctx context.Context) error {
	conn, err := DialHubMTLS(ctx, a.cfg.HubAddr, a.id, a.cfg.HubCAPEM)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := augurv1.NewAgentPlaneServiceClient(conn)

	regResp, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{
		AgentId:     a.agentID,
		AgentType:   a.cfg.AgentType,
		Hostname:    a.cfg.Hostname,
		WorkloadIds: a.cfg.WorkloadIDs,
	})
	if err != nil {
		return fmt.Errorf("identity: register agent: %w", err)
	}
	interval := time.Duration(a.cfg.DefaultIntervalSec) * time.Second
	if regResp.GetMetricsIntervalSec() > 0 {
		interval = time.Duration(regResp.GetMetricsIntervalSec()) * time.Second
	}

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.MetricsStream(connCtx)
	if err != nil {
		return fmt.Errorf("identity: open metrics stream: %w", err)
	}

	recvErr := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				recvErr <- fmt.Errorf("identity: recv goroutine panic: %v", r)
			}
		}()
		for {
			cmd, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			a.handleCommand(connCtx, client, cmd)
		}
	}()

	metricsTicker := time.NewTicker(interval)
	defer metricsTicker.Stop()
	renewTicker := time.NewTicker(renewalCheckInterval)
	defer renewTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			return fmt.Errorf("identity: stream recv: %w", err)
		case <-metricsTicker.C:
			for _, report := range a.cfg.Collect(connCtx, a.agentID, a.cfg.WorkloadIDs) {
				if err := stream.Send(report); err != nil {
					return fmt.Errorf("identity: stream send: %w", err)
				}
			}
		case <-renewTicker.C:
			if reconnect, err := a.maybeRenew(connCtx, client); err != nil {
				logger.Error("Cert renewal failed, will retry: %v", err)
			} else if reconnect {
				return errRenewed
			}
		}
	}
}

// maybeRenew rotates the cert if it has crossed its renewal threshold, persists
// the new identity, and reports whether the caller should reconnect under it.
func (a *Agent) maybeRenew(ctx context.Context, client augurv1.AgentPlaneServiceClient) (bool, error) {
	due, err := a.id.NeedsRenewal(time.Now(), a.cfg.RenewAt)
	if err != nil || !due {
		return false, err
	}
	newID, err := Renew(ctx, client, a.id)
	if err != nil {
		return false, err
	}
	if err := a.store.SaveIdentity(newID.KeyPEM, newID.CertPEM); err != nil {
		return false, err
	}
	a.id = newID
	logger.Info("Cert renewed for agent %s", a.agentID)
	return true, nil
}

// handleCommand runs the binary's scaling callback and reports the outcome.
func (a *Agent) handleCommand(ctx context.Context, client augurv1.AgentPlaneServiceClient, cmd *augurv1.ScalingCommand) {
	outcome := a.cfg.ExecuteScaling(ctx, cmd)
	if outcome == nil {
		return
	}
	if _, err := client.ReportOutcome(ctx, outcome); err != nil {
		logger.Error("Failed to report outcome: %v", err)
	}
}
