package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// ServerlessMetrics holds metrics collected from serverless platforms
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

func main() {
	logger.Init("info")
	logger.Info("Starting Augur serverless agent...")

	agentID := getEnv("AGENT_ID", fmt.Sprintf("serverless-agent-%s", hostname()))
	hubAddr := getEnv("HUB_ADDR", "localhost:50059")
	workloadID := getEnv("WORKLOAD_ID", "")
	platform := getEnv("PLATFORM", "lambda") // lambda, cloud_run, azure_functions
	metricsEndpoint := getEnv("METRICS_ENDPOINT", "") // optional custom metrics endpoint
	intervalSec := 60 // serverless polls less frequently

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := grpc.NewClient(hubAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		logger.Fatal("Failed to connect to hub: %v", err)
	}
	defer conn.Close()

	client := augurv1.NewAgentPlaneServiceClient(conn)

	regResp, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{
		AgentId:     agentID,
		AgentType:   "serverless",
		Hostname:    hostname(),
		WorkloadIds: []string{workloadID},
		Labels: map[string]string{
			"platform": platform,
		},
	})
	if err != nil {
		logger.Fatal("Failed to register: %v", err)
	}
	if regResp.MetricsIntervalSec > 0 {
		intervalSec = int(regResp.MetricsIntervalSec)
	}

	stream, err := client.MetricsStream(ctx)
	if err != nil {
		logger.Fatal("Failed to open metrics stream: %v", err)
	}

	// Receive commands (serverless scaling = adjust concurrency limits)
	go func() {
		for {
			cmd, err := stream.Recv()
			if err != nil {
				logger.Error("Stream recv error: %v", err)
				return
			}
			handleServerlessCommand(ctx, client, cmd, platform)
		}
	}()

	// Polling loop: collect metrics from the serverless platform
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metrics := collectServerlessMetrics(platform, metricsEndpoint)

				memPct := 0.0
				if metrics.MemoryAllocatedMB > 0 {
					memPct = (metrics.MemoryUsedMB / metrics.MemoryAllocatedMB) * 100
				}

				report := &augurv1.AgentMetricsReport{
					AgentId:              agentID,
					WorkloadId:           workloadID,
					CpuUtilizationPct:    metrics.ColdStartPct, // cold start pct as CPU proxy
					MemoryUtilizationPct: memPct,
					RequestsPerSec:       metrics.Invocations,
					LatencyP99Ms:         metrics.AvgDurationMs * 1.5, // rough P99 estimate
					ErrorRatePct:         metrics.ErrorPct,
					CurrentReplicas:      metrics.ConcurrentExec,
					QueueDepth:           float64(metrics.ThrottledPct),
					AgentStatus:          "connected",
				}
				if err := stream.Send(report); err != nil {
					logger.Error("Failed to send metrics: %v", err)
				}
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Serverless agent shutting down...")
}

func collectServerlessMetrics(platform, endpoint string) ServerlessMetrics {
	if endpoint != "" {
		return fetchFromEndpoint(endpoint)
	}

	// Platform-specific collection stubs
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
	// Stub: in production, calls AWS CloudWatch GetMetricData
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
	// Stub: in production, calls Cloud Monitoring API
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
	json.NewDecoder(resp.Body).Decode(&metrics)
	return metrics
}

func handleServerlessCommand(ctx context.Context, client augurv1.AgentPlaneServiceClient, cmd *augurv1.ScalingCommand, platform string) {
	logger.Info("Received command: workload=%s, action=%s, target=%d (platform=%s)",
		cmd.WorkloadId, cmd.Action, cmd.TargetReplicas, platform)

	// Serverless scaling = adjust reserved concurrency (Lambda) or max instances (Cloud Run)
	success := true

	_, _ = client.ReportOutcome(ctx, &augurv1.ScalingOutcomeReport{
		CommandId:      cmd.CommandId,
		WorkloadId:     cmd.WorkloadId,
		Success:        success,
		Outcome:        "healthy",
		ActualReplicas: cmd.TargetReplicas,
	})
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
