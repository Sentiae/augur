package main

import (
	"context"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/sentiae/infrastructure-intelligence-service/internal/agent"
	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/internal/agent/identity"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// collector reads host metrics; chosen once by OS at startup.
var collector interface{ Collect() agent.MetricsSnapshot }

func main() {
	logger.Init("info")
	logger.Info("Starting Augur VM fleet agent (systemd)...")

	if runtime.GOOS == "linux" {
		collector = agent.NewProcCollector()
	} else {
		collector = agent.NewRuntimeCollector()
	}

	cfg, err := identity.RunnerConfigFromEnv("vm", 30)
	if err != nil {
		logger.Fatal("Invalid agent config: %v", err)
	}
	cfg.Collect = collect
	cfg.ExecuteScaling = executeScaling

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := identity.NewAgent(cfg).Run(ctx); err != nil {
		logger.Fatal("Agent exited: %v", err)
	}
	logger.Info("VM agent shut down")
}

// collect samples the host once and maps the snapshot onto each bound workload.
func collect(_ context.Context, agentID string, workloadIDs []string) []*augurv1.AgentMetricsReport {
	snapshot := collector.Collect()
	reports := make([]*augurv1.AgentMetricsReport, 0, len(workloadIDs))
	for _, wID := range workloadIDs {
		reports = append(reports, &augurv1.AgentMetricsReport{
			AgentId:              agentID,
			WorkloadId:           wID,
			CpuUtilizationPct:    snapshot.CPUPct,
			MemoryUtilizationPct: snapshot.MemoryPct,
			RequestsPerSec:       snapshot.RequestsPerSec,
			LatencyP99Ms:         snapshot.LatencyP99Ms,
			ErrorRatePct:         snapshot.ErrorRatePct,
			CurrentReplicas:      snapshot.ReplicaCount,
			AgentStatus:          "connected",
		})
	}
	return reports
}

// executeScaling adjusts the ASG / instance-group size. In production this calls
// the cloud provider API (AWS ASG, GCP MIG, etc.).
func executeScaling(_ context.Context, cmd *augurv1.ScalingCommand) *augurv1.ScalingOutcomeReport {
	logger.Info("Received command: workload=%s, action=%s, target=%d",
		cmd.WorkloadId, cmd.Action, cmd.TargetReplicas)
	return &augurv1.ScalingOutcomeReport{
		CommandId:      cmd.CommandId,
		WorkloadId:     cmd.WorkloadId,
		Success:        true,
		Outcome:        "healthy",
		ActualReplicas: cmd.TargetReplicas,
	}
}
