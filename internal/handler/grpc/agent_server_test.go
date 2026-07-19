package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	internalgrpc "github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc"
)

// fakeOrgResolver is a test double for the D-072 by-id org resolver. It returns
// the configured org (uuid.Nil ⇒ the "no such row" miss path) without touching a
// database, so handler-level org-scoping logic can be exercised directly.
type fakeOrgResolver struct {
	decisionOrg uuid.UUID
	workloadOrg uuid.UUID
}

func (f fakeOrgResolver) ResolveWorkloadOrg(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.workloadOrg, nil
}

func (f fakeOrgResolver) ResolveDecisionOrg(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.decisionOrg, nil
}

// TestAgentServer_ReportOutcome_UnknownDecision — a command_id whose decision the
// resolver cannot map to an org (uuid.Nil) yields codes.NotFound so a cross-org
// id can never probe existence (D-072). The resolve runs before RecordOutcome, so
// the nil decision engine is never reached. Called directly (not over the gRPC
// server) to bypass the pre-existing auth-interceptor rejection.
func TestAgentServer_ReportOutcome_UnknownDecision(t *testing.T) {
	srv := internalgrpc.NewAgentServer(nil, nil, nil, nil, nil, fakeOrgResolver{decisionOrg: uuid.Nil}, nil, nil)

	_, err := srv.ReportOutcome(context.Background(), &augurv1.ScalingOutcomeReport{
		CommandId: uuid.New().String(),
		Outcome:   "healthy",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for unknown decision, got %v", err)
	}
}

// TestAgentServer_ReportOutcome_RejectsInvalidCommandID — a non-uuid command_id is
// an InvalidArgument before any org resolution.
func TestAgentServer_ReportOutcome_RejectsInvalidCommandID(t *testing.T) {
	srv := internalgrpc.NewAgentServer(nil, nil, nil, nil, nil, fakeOrgResolver{}, nil, nil)

	_, err := srv.ReportOutcome(context.Background(), &augurv1.ScalingOutcomeReport{
		CommandId: "not-a-uuid",
		Outcome:   "healthy",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad command_id, got %v", err)
	}
}

// TestAgentServer_GetAgentStatus_Unknown — unregistered agent_id returns
// connected=false without erroring. B23: callers want a cheap heartbeat
// probe that doesn't care whether the agent has ever registered.
func TestAgentServer_GetAgentStatus_Unknown(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := augurv1.NewControlPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.GetAgentStatus(ctx, &augurv1.GetAgentStatusRequest{AgentId: "no-such-agent"})
	if err != nil {
		t.Fatalf("GetAgentStatus: %v", err)
	}
	if resp.Connected {
		t.Fatalf("expected connected=false for unknown agent")
	}
}

// TestAgentServer_GetAgentStatus_Registered — after RegisterAgent the
// lookup reports agent_type + hostname. Connection flag stays false
// because no MetricsStream has been opened yet.
func TestAgentServer_GetAgentStatus_Registered(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	// RegisterAgent is an agent-plane RPC, GetAgentStatus a control-plane RPC
	// (D-177): the split services share the single listener, so use one client
	// per plane over the same connection.
	agentClient := augurv1.NewAgentPlaneServiceClient(conn)
	client := augurv1.NewControlPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := agentClient.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{
		AgentId:     "edge-42",
		AgentType:   "vm",
		Hostname:    "host-abc",
		WorkloadIds: []string{"wl-1", "wl-2"},
	}); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	resp, err := client.GetAgentStatus(ctx, &augurv1.GetAgentStatusRequest{AgentId: "edge-42"})
	if err != nil {
		t.Fatalf("GetAgentStatus: %v", err)
	}
	if resp.AgentType != "vm" {
		t.Fatalf("agent_type mismatch: %q", resp.AgentType)
	}
	if resp.Hostname != "host-abc" {
		t.Fatalf("hostname mismatch: %q", resp.Hostname)
	}
	if len(resp.WorkloadIds) != 2 {
		t.Fatalf("workload_ids: expected 2, got %d", len(resp.WorkloadIds))
	}
}

// TestAgentServer_GetAgentStatus_RejectsEmpty — empty agent_id is a
// precondition violation, not a silent no-op.
func TestAgentServer_GetAgentStatus_RejectsEmpty(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := augurv1.NewControlPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.GetAgentStatus(ctx, &augurv1.GetAgentStatusRequest{})
	if err == nil {
		t.Fatalf("expected error for empty agent_id, got nil")
	}
}

// TestAgentServer_DispatchDeploy_AgentNotConnected — dispatching to an
// unregistered agent is a FailedPrecondition error. Caller should fall
// back to REST / agent-based polling.
func TestAgentServer_DispatchDeploy_AgentNotConnected(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := augurv1.NewControlPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.DispatchDeploy(ctx, &augurv1.DispatchDeployRequest{
		AgentId:      "ghost-agent",
		ArtifactUrl:  "https://example.com/build.tar.gz",
		DeploymentId: "dep-1",
	})
	if err == nil {
		t.Fatalf("expected error dispatching to disconnected agent")
	}
}

// TestAgentServer_DispatchDeploy_RequiresArtifact — missing artifact_url
// is an InvalidArgument. Script is optional, URL is not.
func TestAgentServer_DispatchDeploy_RequiresArtifact(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := augurv1.NewControlPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.DispatchDeploy(ctx, &augurv1.DispatchDeployRequest{
		AgentId: "any",
	})
	if err == nil {
		t.Fatalf("expected InvalidArgument for missing artifact_url")
	}
}
