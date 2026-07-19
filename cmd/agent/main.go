package main

import (
	"context"
	"math/rand"
	"os/signal"
	"syscall"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/internal/agent/identity"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

func main() {
	logger.Init("info")
	logger.Info("Starting Augur edge agent...")

	cfg, err := identity.RunnerConfigFromEnv("kubernetes", 30)
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
	logger.Info("Edge agent shut down")
}

// collect gathers current metrics for each bound workload.
func collect(_ context.Context, agentID string, workloadIDs []string) []*augurv1.AgentMetricsReport {
	reports := make([]*augurv1.AgentMetricsReport, 0, len(workloadIDs))
	for _, wID := range workloadIDs {
		reports = append(reports, &augurv1.AgentMetricsReport{
			AgentId:              agentID,
			WorkloadId:           wID,
			CpuUtilizationPct:    readCPU(),
			MemoryUtilizationPct: readMemory(),
			RequestsPerSec:       readRequestRate(),
			LatencyP99Ms:         readLatency(),
			ErrorRatePct:         readErrorRate(),
			CurrentReplicas:      readReplicaCount(),
			AgentStatus:          "connected",
		})
	}
	return reports
}

// executeScaling runs the scaling action and returns the outcome to report, or
// nil for a dry run (nothing to report).
func executeScaling(_ context.Context, cmd *augurv1.ScalingCommand) *augurv1.ScalingOutcomeReport {
	logger.Info("Received scaling command: workload=%s, action=%s, target=%d, reason=%s",
		cmd.WorkloadId, cmd.Action, cmd.TargetReplicas, cmd.Reasoning)

	if cmd.DryRun {
		logger.Info("Dry run — not executing")
		return nil
	}

	// In production, this would call the K8s HPA / Firecracker API, etc.
	logger.Info("Executing scale: workload=%s → %d replicas", cmd.WorkloadId, cmd.TargetReplicas)
	success := true

	outcome := "healthy"
	errMsg := ""
	if !success {
		outcome = "failed"
		errMsg = "scaling action failed"
	}
	return &augurv1.ScalingOutcomeReport{
		CommandId:      cmd.CommandId,
		WorkloadId:     cmd.WorkloadId,
		Success:        success,
		Outcome:        outcome,
		ErrorMessage:   errMsg,
		ActualReplicas: cmd.TargetReplicas,
	}
}

// Metric collection stubs — in production, these read from the actual infrastructure.
func readCPU() float64        { return 30 + rand.Float64()*40 }
func readMemory() float64     { return 40 + rand.Float64()*30 }
func readRequestRate() float64 { return 100 + rand.Float64()*500 }
func readLatency() float64    { return 50 + rand.Float64()*200 }
func readErrorRate() float64  { return rand.Float64() * 0.5 }
func readReplicaCount() int32 { return 3 }
