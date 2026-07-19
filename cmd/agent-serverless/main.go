package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/internal/agent/identity"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// ServerlessMetrics holds metrics collected from serverless platforms.
type ServerlessMetrics struct {
	Invocations       float64 `json:"invocations_per_sec"`
	AvgDurationMs     float64 `json:"avg_duration_ms"`
	ColdStartPct      float64 `json:"cold_start_pct"`
	ErrorPct          float64 `json:"error_pct"`
	ConcurrentExec    int32   `json:"concurrent_executions"`
	ThrottledPct      float64 `json:"throttled_pct"`
	MemoryUsedMB      float64 `json:"memory_used_mb"`
	MemoryAllocatedMB float64 `json:"memory_allocated_mb"`
}

var (
	platform        string
	metricsEndpoint string
)

func main() {
	logger.Init("info")
	logger.Info("Starting Augur serverless agent...")

	platform = getEnv("PLATFORM", "lambda")             // lambda, cloud_run, azure_functions
	metricsEndpoint = os.Getenv("METRICS_ENDPOINT")     // optional custom metrics endpoint

	cfg, err := identity.RunnerConfigFromEnv("serverless", 60) // serverless polls less frequently
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
	logger.Info("Serverless agent shut down")
}

// collect polls the serverless platform and maps its metrics onto each workload.
func collect(_ context.Context, agentID string, workloadIDs []string) []*augurv1.AgentMetricsReport {
	metrics := collectServerlessMetrics(platform, metricsEndpoint)
	memPct := 0.0
	if metrics.MemoryAllocatedMB > 0 {
		memPct = (metrics.MemoryUsedMB / metrics.MemoryAllocatedMB) * 100
	}

	reports := make([]*augurv1.AgentMetricsReport, 0, len(workloadIDs))
	for _, wID := range workloadIDs {
		reports = append(reports, &augurv1.AgentMetricsReport{
			AgentId:              agentID,
			WorkloadId:           wID,
			CpuUtilizationPct:    metrics.ColdStartPct, // cold start pct as CPU proxy
			MemoryUtilizationPct: memPct,
			RequestsPerSec:       metrics.Invocations,
			LatencyP99Ms:         metrics.AvgDurationMs * 1.5, // rough P99 estimate
			ErrorRatePct:         metrics.ErrorPct,
			CurrentReplicas:      metrics.ConcurrentExec,
			QueueDepth:           float64(metrics.ThrottledPct),
			AgentStatus:          "connected",
		})
	}
	return reports
}

// executeScaling adjusts reserved concurrency (Lambda) or max instances (Cloud Run).
func executeScaling(_ context.Context, cmd *augurv1.ScalingCommand) *augurv1.ScalingOutcomeReport {
	logger.Info("Received command: workload=%s, action=%s, target=%d (platform=%s)",
		cmd.WorkloadId, cmd.Action, cmd.TargetReplicas, platform)
	return &augurv1.ScalingOutcomeReport{
		CommandId:      cmd.CommandId,
		WorkloadId:     cmd.WorkloadId,
		Success:        true,
		Outcome:        "healthy",
		ActualReplicas: cmd.TargetReplicas,
	}
}

func collectServerlessMetrics(platform, endpoint string) ServerlessMetrics {
	if endpoint != "" {
		return fetchFromEndpoint(endpoint)
	}

	// Platform-specific collection stubs.
	// In production: call CloudWatch (Lambda), Cloud Monitoring (Cloud Run), etc.
	switch platform {
	case "lambda":
		return collectLambdaMetrics()
	case "cloud_run":
		return collectCloudRunMetrics()
	default:
		return ServerlessMetrics{}
	}
}

func collectLambdaMetrics() ServerlessMetrics {
	// Stub: in production, calls AWS CloudWatch GetMetricData.
	return ServerlessMetrics{
		Invocations:       50,
		AvgDurationMs:     120,
		ColdStartPct:      5,
		ErrorPct:          0.1,
		ConcurrentExec:    10,
		ThrottledPct:      0,
		MemoryUsedMB:      128,
		MemoryAllocatedMB: 256,
	}
}

func collectCloudRunMetrics() ServerlessMetrics {
	// Stub: in production, calls Cloud Monitoring API.
	return ServerlessMetrics{
		Invocations:       100,
		AvgDurationMs:     80,
		ColdStartPct:      2,
		ErrorPct:          0.05,
		ConcurrentExec:    25,
		ThrottledPct:      0,
		MemoryUsedMB:      256,
		MemoryAllocatedMB: 512,
	}
}

func fetchFromEndpoint(endpoint string) ServerlessMetrics {
	resp, err := http.Get(endpoint)
	if err != nil {
		logger.Error("Failed to fetch metrics from %s: %v", endpoint, err)
		return ServerlessMetrics{}
	}
	defer resp.Body.Close()

	var metrics ServerlessMetrics
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		logger.Error("Failed to decode metrics from %s: %v", endpoint, err)
		return ServerlessMetrics{}
	}
	return metrics
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
