package grpc

import (
	"context"
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
) *AgentServer {
	return &AgentServer{
		workloadRepo: workloadRepo,
		policyRepo:   policyRepo,
		decisionRepo: decisionRepo,
		workloadSvc:  workloadSvc,
		decisionEng:  decisionEng,
		orgResolver:  orgResolver,
		agents:       make(map[string]*connectedAgent),
	}
}

// RegisterServer registers both gRPC services with a gRPC server. Per D-177
// the agent plane and control plane are distinct services; P5 will split
// them onto separate listeners with different TLS, but for now both register
// on the single provided server so the service keeps working.
func (s *AgentServer) RegisterServer(srv *grpc.Server) {
	augurv1.RegisterAgentPlaneServiceServer(srv, s)
	augurv1.RegisterControlPlaneServiceServer(srv, s)
}

// RegisterAgent handles edge agent registration
func (s *AgentServer) RegisterAgent(ctx context.Context, req *augurv1.RegisterAgentRequest) (*augurv1.RegisterAgentResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	s.mu.Lock()
	s.agents[req.AgentId] = &connectedAgent{
		agentID:   req.AgentId,
		agentType: req.AgentType,
		hostname:  req.Hostname,
		lastSeen:  time.Now(),
		workloads: req.WorkloadIds,
	}
	s.mu.Unlock()

	logger.Info("Edge agent registered: id=%s, type=%s, host=%s, workloads=%d",
		req.AgentId, req.AgentType, req.Hostname, len(req.WorkloadIds))

	return &augurv1.RegisterAgentResponse{
		Success:            true,
		Message:            "Agent registered successfully",
		MetricsIntervalSec: 30,
		PolicyVersion:      "v1",
	}, nil
}

// MetricsStream handles bidirectional streaming between edge agents and control plane
func (s *AgentServer) MetricsStream(stream augurv1.AgentPlaneService_MetricsStreamServer) error {
	var agentID string

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

		agentID = report.AgentId

		// Update agent last seen
		s.mu.Lock()
		if agent, ok := s.agents[agentID]; ok {
			agent.lastSeen = time.Now()
			agent.stream = stream
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
	if p, ok := tenant.FromContext(ctx); ok && p.Claims != nil {
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

	// Layer-2 tenant isolation: the client supplies the target org, so verify
	// the caller may act in it before reading its policies. Service principals
	// (edge agents authenticated by x-api-key) pass; a user principal must be a
	// member of orgID.
	if err := tenant.AuthorizeOrg(ctx, orgID); err != nil {
		return nil, err
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
