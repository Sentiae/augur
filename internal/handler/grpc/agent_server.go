package grpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sentiae/platform-kit/tenant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc/agentprincipal"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/internal/usecase"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// OrgResolver resolves the owning org of a by-id resource via the D-072 SECURITY
// DEFINER rls_* functions, so an id-only handler (no organization_id in the
// request) can scope RLS without trusting caller input. Satisfied by the
// postgres TenantResolverRepo; declared locally so the interface — not the
// concrete repo — is the handler's dependency (DI passes the concrete impl).
type OrgResolver interface {
	ResolveWorkloadOrg(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	ResolveDecisionOrg(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
}

// AgentFinder loads an enrolled agent by id. The AgentPrincipalInterceptor uses
// it to cross-check a presented client cert against the enrolled row; the
// agent-plane data handlers use it to enforce workload bindings. Satisfied by the
// postgres AgentRepository; declared as an interface so both the interceptor and
// handler depend on the seam, not the concrete repo.
type AgentFinder interface {
	FindAgentByID(ctx context.Context, id uuid.UUID) (*domain.Agent, error)
}

// AgentServer implements both the AgentPlaneService and ControlPlaneService
// gRPC services. The two services are split per D-177 (least-privilege: an
// edge agent's generated client cannot name the control-plane RPCs); one
// handler satisfies both server interfaces. The embedded Unimplemented*
// servers supply the default Enroll/Renew methods (codes.Unimplemented)
// until the enrollment logic lands in P4.
type AgentServer struct {
	augurv1.UnimplementedAgentPlaneServiceServer
	augurv1.UnimplementedControlPlaneServiceServer

	workloadRepo *postgres.WorkloadRepository
	policyRepo   *postgres.PolicyRepository
	decisionRepo *postgres.DecisionRepository
	workloadSvc  *usecase.WorkloadService
	decisionEng  *usecase.DecisionEngine
	orgResolver  OrgResolver
	enrollment   *usecase.AgentEnrollmentService
	agentFinder  AgentFinder

	// enforceIdentity gates the P5b cert-derived-identity hardening on the four
	// agent-plane data RPCs. Set true by DI ONLY when the agent plane is enabled
	// (the data RPCs are then reachable exclusively over the mTLS listener where
	// the AgentPrincipalInterceptor injects a principal). Left false when the plane
	// is disabled so the RPCs behave byte-identically to today on the plaintext
	// listener (no mTLS interceptor ⇒ no principal to derive from).
	enforceIdentity bool

	// Track connected agents
	mu     sync.RWMutex
	agents map[string]*connectedAgent
}

type connectedAgent struct {
	agentID   string
	agentType string
	hostname  string
	stream    augurv1.AgentPlaneService_MetricsStreamServer
	lastSeen  time.Time
	workloads []string
}

func NewAgentServer(
	workloadRepo *postgres.WorkloadRepository,
	policyRepo *postgres.PolicyRepository,
	decisionRepo *postgres.DecisionRepository,
	workloadSvc *usecase.WorkloadService,
	decisionEng *usecase.DecisionEngine,
	orgResolver OrgResolver,
	enrollment *usecase.AgentEnrollmentService,
	agentFinder AgentFinder,
) *AgentServer {
	return &AgentServer{
		workloadRepo: workloadRepo,
		policyRepo:   policyRepo,
		decisionRepo: decisionRepo,
		workloadSvc:  workloadSvc,
		decisionEng:  decisionEng,
		orgResolver:  orgResolver,
		enrollment:   enrollment,
		agentFinder:  agentFinder,
		agents:       make(map[string]*connectedAgent),
	}
}

// EnableIdentityEnforcement turns on the P5b cert-derived-identity hardening. DI
// calls it only when the agent plane is enabled, so the four data RPCs enforce
// the mTLS principal (org + workload bindings) and fail closed on its absence.
func (s *AgentServer) EnableIdentityEnforcement() {
	s.enforceIdentity = true
}

// RegisterServer registers BOTH gRPC services on one listener. Used only when the
// agent plane is DISABLED (byte-identical to today: both planes on the plaintext
// listener). When the plane is enabled DI splits them — RegisterControlPlaneOnly
// on the plaintext listener, the agent plane on the mTLS listener (D-177).
func (s *AgentServer) RegisterServer(srv *grpc.Server) {
	augurv1.RegisterAgentPlaneServiceServer(srv, s)
	augurv1.RegisterControlPlaneServiceServer(srv, s)
}

// RegisterControlPlaneOnly registers ONLY the ControlPlaneService. Used on the
// plaintext listener when the agent plane is enabled, so an agent (which reaches
// only the mTLS listener) can never name a control-plane RPC (D-177).
func (s *AgentServer) RegisterControlPlaneOnly(srv *grpc.Server) {
	augurv1.RegisterControlPlaneServiceServer(srv, s)
}

// Enroll (AgentPlaneService) exchanges a one-time join token + CSR for a signed
// agent certificate. Pre-auth: the caller has no client cert yet (mTLS lands in
// P5), so the enrollment usecase does all lookups under a system ctx and derives
// the org + agent-id from the AGENT ROW, never from this request.
func (s *AgentServer) Enroll(ctx context.Context, req *augurv1.EnrollRequest) (*augurv1.EnrollResponse, error) {
	if req.GetJoinToken() == "" || req.GetCsrPem() == "" {
		return nil, status.Error(codes.InvalidArgument, "join_token and csr_pem are required")
	}
	out, err := s.enrollment.EnrollAgent(ctx, usecase.EnrollAgentInput{
		RawToken:  req.GetJoinToken(),
		CSRPEM:    req.GetCsrPem(),
		AgentType: req.GetAgentType(),
		Hostname:  req.GetHostname(),
	})
	if err != nil {
		return nil, enrollErrToStatus(err)
	}
	return &augurv1.EnrollResponse{
		CertPem:            out.CertPEM,
		CaBundlePem:        out.CAChainPEM,
		AgentId:            out.AgentID.String(),
		MetricsIntervalSec: out.MetricsIntervalSec,
	}, nil
}

// Renew (AgentPlaneService) rotates the calling agent's certificate. The agent
// id MUST come from the authenticated mTLS AgentPrincipal (P5), NOT the request
// body (which would be forgeable). Until P5 populates the principal this fails
// closed with Unauthenticated.
func (s *AgentServer) Renew(ctx context.Context, req *augurv1.RenewRequest) (*augurv1.RenewResponse, error) {
	agentID, ok := agentprincipal.AgentIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated agent principal")
	}
	if req.GetCsrPem() == "" {
		return nil, status.Error(codes.InvalidArgument, "csr_pem is required")
	}
	out, err := s.enrollment.RenewAgentCert(ctx, usecase.RenewAgentCertInput{
		AgentID: agentID,
		CSRPEM:  req.GetCsrPem(),
	})
	if err != nil {
		return nil, enrollErrToStatus(err)
	}
	return &augurv1.RenewResponse{CertPem: out.CertPEM}, nil
}

// PreRegisterAgent (ControlPlaneService) creates a pending agent and mints its
// single-use enrollment token, returning the raw token once. Operator/ops action
// — runs under the caller's authenticated org ctx.
func (s *AgentServer) PreRegisterAgent(ctx context.Context, req *augurv1.PreRegisterAgentRequest) (*augurv1.PreRegisterAgentResponse, error) {
	orgID, err := uuid.Parse(req.GetOrganizationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid organization_id")
	}
	if req.GetAgentType() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_type is required")
	}
	out, err := s.enrollment.PreRegisterAgent(ctx, usecase.PreRegisterAgentInput{
		OrgID:            orgID,
		AgentType:        req.GetAgentType(),
		Hostname:         req.GetHostname(),
		WorkloadBindings: req.GetWorkloadIds(),
		TokenTTL:         time.Duration(req.GetTokenTtlSec()) * time.Second,
	})
	if err != nil {
		return nil, enrollErrToStatus(err)
	}
	return &augurv1.PreRegisterAgentResponse{
		AgentId:         out.AgentID.String(),
		EnrollmentToken: out.RawToken,
	}, nil
}

// RevokeAgent (ControlPlaneService) marks an agent identity revoked. Runs under
// the caller's authenticated org ctx (the row is org-scoped so RLS confines it).
func (s *AgentServer) RevokeAgent(ctx context.Context, req *augurv1.RevokeAgentRequest) (*augurv1.RevokeAgentResponse, error) {
	agentID, err := uuid.Parse(req.GetAgentId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid agent_id")
	}
	if err := s.enrollment.RevokeAgent(ctx, usecase.RevokeAgentInput{AgentID: agentID}); err != nil {
		return nil, enrollErrToStatus(err)
	}
	return &augurv1.RevokeAgentResponse{Revoked: true}, nil
}

// enrollErrToStatus maps enrollment usecase/domain errors to gRPC status codes at
// the handler boundary (root §16). A bad/expired/used token collapses to
// PermissionDenied so it never leaks whether the token existed.
func enrollErrToStatus(err error) error {
	switch {
	case errors.Is(err, usecase.ErrAgentPlaneDisabled):
		return status.Error(codes.Unavailable, "agent plane disabled")
	case errors.Is(err, domain.ErrEnrollmentTokenNotFound),
		errors.Is(err, domain.ErrEnrollmentTokenExpired),
		errors.Is(err, domain.ErrEnrollmentTokenConsumed):
		return status.Error(codes.PermissionDenied, "invalid enrollment token")
	case errors.Is(err, domain.ErrAgentRevoked):
		return status.Error(codes.FailedPrecondition, "agent is revoked")
	case errors.Is(err, domain.ErrAgentNotFound):
		return status.Error(codes.NotFound, "agent not found")
	case errors.Is(err, domain.ErrInvalidAgentOrg), errors.Is(err, domain.ErrInvalidAgentType):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, "enrollment failed")
	}
}

// requirePrincipal resolves the mTLS agent identity for the four data RPCs. When
// identity enforcement is OFF (agent plane disabled) it returns enforce=false and
// callers keep today's behavior. When ON, it fails closed: a missing principal
// (Unauthenticated — never happens on the mTLS listener, defense in depth) or an
// agent-row lookup failure aborts before any data access. On success it returns
// the cert-derived principal AND the enrolled agent row (for workload-binding
// checks), so handlers trust the CERT, not the request.
func (s *AgentServer) requirePrincipal(ctx context.Context) (enforce bool, p agentprincipal.Principal, agent *domain.Agent, err error) {
	if !s.enforceIdentity {
		return false, agentprincipal.Principal{}, nil, nil
	}
	pr, ok := agentprincipal.PrincipalFromContext(ctx)
	if !ok {
		return true, agentprincipal.Principal{}, nil, status.Error(codes.Unauthenticated, "no authenticated agent principal")
	}
	a, ferr := s.agentFinder.FindAgentByID(tenant.WithSystemContext(ctx), pr.AgentID)
	if ferr != nil {
		return true, pr, nil, status.Error(codes.Unauthenticated, "agent not found")
	}
	return true, pr, a, nil
}

// RegisterAgent handles edge agent registration. Under identity enforcement the
// connected-agent map is keyed on the AUTHENTICATED agent id (from the cert), not
// the request's agent_id — killing impersonation. The request's agent_id /
// workload_ids are then advisory only.
func (s *AgentServer) RegisterAgent(ctx context.Context, req *augurv1.RegisterAgentRequest) (*augurv1.RegisterAgentResponse, error) {
	enforce, p, agent, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}

	agentKey := req.AgentId
	workloads := req.WorkloadIds
	if enforce {
		agentKey = p.AgentID.String()
		workloads = []string(agent.WorkloadBindings)
	} else if agentKey == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	s.mu.Lock()
	s.agents[agentKey] = &connectedAgent{
		agentID:   agentKey,
		agentType: req.AgentType,
		hostname:  req.Hostname,
		lastSeen:  time.Now(),
		workloads: workloads,
	}
	s.mu.Unlock()

	logger.Info("Edge agent registered: id=%s, type=%s, host=%s, workloads=%d",
		agentKey, req.AgentType, req.Hostname, len(workloads))

	return &augurv1.RegisterAgentResponse{
		Success:            true,
		Message:            "Agent registered successfully",
		MetricsIntervalSec: 30,
		PolicyVersion:      "v1",
	}, nil
}

// MetricsStream handles bidirectional streaming between edge agents and control plane
func (s *AgentServer) MetricsStream(stream augurv1.AgentPlaneService_MetricsStreamServer) error {
	// Resolve the mTLS identity ONCE per connection (bindings don't change
	// mid-stream; a revoke is enforced at the next connect by the interceptor).
	enforce, p, agent, perr := s.requirePrincipal(stream.Context())
	if perr != nil {
		return perr
	}

	var agentID string
	if enforce {
		agentID = p.AgentID.String()
	}

	for {
		report, err := stream.Recv()
		if err == io.EOF {
			logger.Info("Edge agent stream closed: %s", agentID)
			return nil
		}
		if err != nil {
			logger.Error("Edge agent stream error: %v", err)
			return err
		}

		if !enforce {
			// Legacy path: trust the report's agent id for the connection map key.
			agentID = report.AgentId
		}

		// Update agent last seen
		s.mu.Lock()
		if a, ok := s.agents[agentID]; ok {
			a.lastSeen = time.Now()
			a.stream = stream
		}
		s.mu.Unlock()

		// Process metrics
		if report.WorkloadId != "" {
			wID, err := uuid.Parse(report.WorkloadId)
			if err == nil {
				// D-072 RLS: resolve the workload's real org and scope the per-message
				// ctx before the RLS-forced write. The caller is an x-api-key service
				// principal, so this is the D-071 job shape (WithSystemOrg) — the
				// resolver validated the workload's owner, so it is not a spoof. An
				// unknown workload (uuid.Nil) is logged and skipped so one bad id
				// never kills the whole stream.
				org, rerr := s.orgResolver.ResolveWorkloadOrg(stream.Context(), wID)
				if rerr != nil {
					logger.Error("Failed to resolve org for workload %s: %v", report.WorkloadId, rerr)
					continue
				}
				if org == uuid.Nil {
					logger.Warn("Skipping metrics for unknown workload %s", report.WorkloadId)
					continue
				}
				if enforce && (org != p.OrgID || !agent.HasWorkload(report.WorkloadId)) {
					// Cross-tenant or unbound workload: drop silently (no existence
					// leak) — one report never crosses the agent's cert-scoped org +
					// bindings.
					logger.Warn("agent-plane: dropping metrics for workload %s not owned/bound by agent %s", report.WorkloadId, agentID)
					continue
				}
				msgCtx := tenant.WithSystemOrg(stream.Context(), org)

				snapshot := &domain.WorkloadMetricsSnapshot{
					CPUPct:         report.CpuUtilizationPct,
					MemoryPct:      report.MemoryUtilizationPct,
					RequestsPerSec: report.RequestsPerSec,
					LatencyP99Ms:   report.LatencyP99Ms,
					ErrorRatePct:   report.ErrorRatePct,
					Replicas:       int(report.CurrentReplicas),
					Timestamp:      time.Now(),
				}

				if err := s.workloadSvc.UpdateMetrics(msgCtx, wID, snapshot); err != nil {
					logger.Error("Failed to update metrics for workload %s: %v", report.WorkloadId, err)
				}
			}
		}
	}
}

// ReportOutcome handles scaling action outcome reports from edge agents
func (s *AgentServer) ReportOutcome(ctx context.Context, req *augurv1.ScalingOutcomeReport) (*augurv1.ScalingOutcomeResponse, error) {
	if req.CommandId == "" {
		return nil, status.Error(codes.InvalidArgument, "command_id is required")
	}

	enforce, p, agent, perr := s.requirePrincipal(ctx)
	if perr != nil {
		return nil, perr
	}

	decisionID, err := uuid.Parse(req.CommandId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid command_id")
	}

	// D-072 RLS: the report is keyed by a decision id with no org field, so
	// resolve the decision's owning org and scope the ctx before the RLS-forced
	// write. An unknown id (uuid.Nil) yields NotFound so existence never leaks.
	// If a user principal is present its memberships are the sole authority — a
	// cross-org hit also collapses to NotFound.
	org, err := s.orgResolver.ResolveDecisionOrg(ctx, decisionID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "resolve decision org: %v", err)
	}
	if org == uuid.Nil {
		return nil, status.Error(codes.NotFound, "decision not found")
	}
	if enforce {
		// The resolved decision org must match the agent's cert org, and (when the
		// report names a workload) the agent must be bound to it. A mismatch
		// collapses to NotFound so a cross-tenant probe never learns the decision
		// exists.
		if org != p.OrgID || (req.WorkloadId != "" && !agent.HasWorkload(req.WorkloadId)) {
			logger.Warn("agent-plane: dropping outcome for decision %s not owned/bound by agent %s", req.CommandId, p.AgentID)
			return nil, status.Error(codes.NotFound, "decision not found")
		}
	} else if pr, ok := tenant.FromContext(ctx); ok && pr.Claims != nil {
		if err := tenant.AssertOrgOrNotFound(ctx, org, "decision not found"); err != nil {
			return nil, err
		}
	}
	ctx = tenant.WithActiveOrg(ctx, org)

	outcome := domain.DecisionOutcomeHealthy
	switch req.Outcome {
	case "degraded":
		outcome = domain.DecisionOutcomeDegraded
	case "failed":
		outcome = domain.DecisionOutcomeFailed
	case "rolled_back":
		outcome = domain.DecisionOutcomeRolledBack
	}

	if err := s.decisionEng.RecordOutcome(ctx, decisionID, outcome); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to record outcome: %v", err)
	}

	logger.Info("Scaling outcome recorded: command=%s, workload=%s, outcome=%s",
		req.CommandId, req.WorkloadId, req.Outcome)

	return &augurv1.ScalingOutcomeResponse{Acknowledged: true}, nil
}

// GetPolicy returns the resolved policy for a workload
func (s *AgentServer) GetPolicy(ctx context.Context, req *augurv1.GetPolicyRequest) (*augurv1.GetPolicyResponse, error) {
	if req.WorkloadId == "" || req.OrganizationId == "" {
		return nil, status.Error(codes.InvalidArgument, "workload_id and organization_id are required")
	}

	orgID, err := uuid.Parse(req.OrganizationId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid organization_id")
	}

	enforce, p, agent, perr := s.requirePrincipal(ctx)
	if perr != nil {
		return nil, perr
	}

	if enforce {
		// The requested org + workload must belong to the authenticated agent —
		// the cert org, not the request, is the authority. A mismatch collapses to
		// NotFound so a cross-tenant probe never learns the policy exists.
		if orgID != p.OrgID || !agent.HasWorkload(req.WorkloadId) {
			logger.Warn("agent-plane: dropping policy request for org=%s workload=%s not owned/bound by agent %s", req.OrganizationId, req.WorkloadId, p.AgentID)
			return nil, status.Error(codes.NotFound, "policy not found")
		}
	} else {
		// Layer-2 tenant isolation: the client supplies the target org, so verify
		// the caller may act in it before reading its policies. Service principals
		// (edge agents authenticated by x-api-key) pass; a user principal must be a
		// member of orgID.
		if err := tenant.AuthorizeOrg(ctx, orgID); err != nil {
			return nil, err
		}
	}

	// D-072 RLS: stamp the active org before the RLS-forced policy reads. The
	// UnaryOrgField interceptor already stamps it from the request's
	// organization_id field; re-stamping here is harmless and explicit.
	ctx = tenant.WithActiveOrg(ctx, orgID)

	global, _ := s.policyRepo.FindGlobal(ctx, orgID)
	app, _ := s.policyRepo.FindByApp(ctx, orgID, req.WorkloadId)

	resolved := domain.ResolvePolicy(global, nil, app)

	return &augurv1.GetPolicyResponse{
		OptimizationMode:  string(resolved.OptimizationMode),
		MinReplicas:       int32(resolved.MinReplicas),
		MaxReplicas:       int32(resolved.MaxReplicas),
		MaxBudgetUsd:      resolved.MaxBudgetUSD,
		EnableSpot:        resolved.EnableSpot,
		WeightCost:        resolved.Weights.Cost,
		WeightPerformance: resolved.Weights.Performance,
		WeightReliability: resolved.Weights.Reliability,
		PolicyVersion:     "v1",
	}, nil
}

// SendScalingCommand sends a scaling command to a connected edge agent
func (s *AgentServer) SendScalingCommand(agentID string, cmd *augurv1.ScalingCommand) error {
	s.mu.RLock()
	agent, ok := s.agents[agentID]
	s.mu.RUnlock()

	if !ok || agent.stream == nil {
		return fmt.Errorf("agent %s not connected", agentID)
	}

	cmd.CommandId = uuid.New().String()
	cmd.IssuedAt = timestamppb.Now()

	return agent.stream.Send(cmd)
}

// DispatchDeploy forwards a unary deploy instruction to a connected edge
// agent over its active MetricsStream. Added for B23 so ops-service can
// drive customer-hosted agents through augur without an extra REST
// surface on every agent.
//
// The implementation wraps DispatchDeployRequest into a ScalingCommand
// with action="deploy" and the artifact+script payload, then reuses the
// existing stream plumbing. The response echoes the minted command_id so
// callers can correlate with a later ReportOutcome.
func (s *AgentServer) DispatchDeploy(ctx context.Context, req *augurv1.DispatchDeployRequest) (*augurv1.DispatchDeployResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if req.ArtifactUrl == "" {
		return nil, status.Error(codes.InvalidArgument, "artifact_url is required")
	}

	s.mu.RLock()
	agent, ok := s.agents[req.AgentId]
	s.mu.RUnlock()
	if !ok || agent.stream == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "agent %s not connected", req.AgentId)
	}

	commandID := uuid.New().String()
	cmd := &augurv1.ScalingCommand{
		CommandId:    commandID,
		WorkloadId:   req.WorkloadId,
		Action:       "deploy",
		Reasoning:    req.Reasoning,
		Trigger:      "deploy",
		Confidence:   1.0,
		DryRun:       req.DryRun,
		IssuedAt:     timestamppb.Now(),
		ArtifactUrl:  req.ArtifactUrl,
		DeployScript: req.DeployScript,
		Version:      req.Version,
	}

	if err := agent.stream.Send(cmd); err != nil {
		return nil, status.Errorf(codes.Unavailable, "dispatch deploy: %v", err)
	}

	logger.Info("Deploy dispatched: agent=%s deployment=%s command=%s artifact=%s dry_run=%v",
		req.AgentId, req.DeploymentId, commandID, req.ArtifactUrl, req.DryRun)

	return &augurv1.DispatchDeployResponse{
		Dispatched: true,
		CommandId:  commandID,
		Message:    "deploy enqueued to agent",
	}, nil
}

// GetAgentStatus reports connection state and metadata for a single
// registered agent. Returns connected=false when the agent is not
// registered (rather than NotFound) so callers can implement simple
// heartbeat loops without special-casing first-run. B23.
func (s *AgentServer) GetAgentStatus(ctx context.Context, req *augurv1.GetAgentStatusRequest) (*augurv1.GetAgentStatusResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, ok := s.agents[req.AgentId]
	if !ok {
		return &augurv1.GetAgentStatusResponse{Connected: false}, nil
	}
	return &augurv1.GetAgentStatusResponse{
		Connected:   agent.stream != nil,
		AgentType:   agent.agentType,
		Hostname:    agent.hostname,
		LastSeen:    timestamppb.New(agent.lastSeen),
		WorkloadIds: agent.workloads,
	}, nil
}

// ConnectedAgentCount returns the number of connected edge agents
func (s *AgentServer) ConnectedAgentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.agents)
}
