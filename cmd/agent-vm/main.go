package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sentiae/infrastructure-intelligence-service/internal/agent"
	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

func main() {
	logger.Init("info")
	logger.Info("Starting Augur VM fleet agent (systemd)...")

	agentID := getEnv("AGENT_ID", fmt.Sprintf("vm-agent-%s", hostname()))
	hubAddr := getEnv("HUB_ADDR", "localhost:50059")
	workloadID := getEnv("WORKLOAD_ID", "")
	intervalSec := 30

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to control plane
	conn, err := grpc.NewClient(hubAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		logger.Fatal("Failed to connect to hub: %v", err)
	}
	defer conn.Close()

	client := augurv1.NewAugurAgentServiceClient(conn)

	// Register
	regResp, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{
		AgentId:     agentID,
		AgentType:   "vm",
		Hostname:    hostname(),
		WorkloadIds: []string{workloadID},
	})
	if err != nil {
		logger.Fatal("Failed to register: %v", err)
	}
	if regResp.MetricsIntervalSec > 0 {
		intervalSec = int(regResp.MetricsIntervalSec)
	}

	// Choose collector based on OS
	var collector interface{ Collect() agent.MetricsSnapshot }
	if runtime.GOOS == "linux" {
		collector = agent.NewProcCollector()
	} else {
		collector = agent.NewRuntimeCollector()
	}

	// Setup degraded mode
	degraded := agent.NewDegradedMode(10) // default max from registration

	// Open metrics stream
	stream, err := client.MetricsStream(ctx)
	if err != nil {
		logger.Fatal("Failed to open metrics stream: %v", err)
	}

	// Receive commands in background
	go func() {
		for {
			cmd, err := stream.Recv()
			if err != nil {
				logger.Error("Stream recv error, entering degraded mode: %v", err)
				degraded.Enter()
				return
			}
			handleCommand(ctx, client, cmd)
		}
	}()

	// Metrics collection loop
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snapshot := collector.Collect()

				if degraded.IsActive() {
					// In degraded mode: only emergency scale-up
					if _, shouldScale := degraded.EvaluateScaleUp(snapshot, int(snapshot.ReplicaCount)); shouldScale {
						logger.Warn("Degraded mode scale-up triggered")
					}
					continue
				}

				if workloadID != "" {
					report := &augurv1.AgentMetricsReport{
						AgentId:              agentID,
						WorkloadId:           workloadID,
						CpuUtilizationPct:    snapshot.CPUPct,
						MemoryUtilizationPct: snapshot.MemoryPct,
						RequestsPerSec:       snapshot.RequestsPerSec,
						LatencyP99Ms:         snapshot.LatencyP99Ms,
						ErrorRatePct:         snapshot.ErrorRatePct,
						CurrentReplicas:      snapshot.ReplicaCount,
						AgentStatus:          "connected",
					}
					if err := stream.Send(report); err != nil {
						logger.Error("Failed to send metrics: %v", err)
						degraded.Enter()
					}
				}
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("VM agent shutting down...")
}

func handleCommand(ctx context.Context, client augurv1.AugurAgentServiceClient, cmd *augurv1.ScalingCommand) {
	logger.Info("Received command: workload=%s, action=%s, target=%d",
		cmd.WorkloadId, cmd.Action, cmd.TargetReplicas)

	// VM fleet scaling: adjust ASG/instance group size
	// In production: call cloud provider API (AWS ASG, GCP MIG, etc.)
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
