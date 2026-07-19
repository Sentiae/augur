package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

func main() {
	logger.Init("info")
	logger.Info("Starting Augur edge agent...")

	agentID := getEnv("AGENT_ID", fmt.Sprintf("agent-%d", rand.Intn(10000)))
	agentType := getEnv("AGENT_TYPE", "kubernetes")
	hubAddr := getEnv("HUB_ADDR", "localhost:50059")
	hostname, _ := os.Hostname()
	workloadIDs := strings.Split(getEnv("WORKLOAD_IDS", ""), ",")
	intervalSec := 30

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to control plane
	conn, err := grpc.DialContext(ctx, hubAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(10*time.Second),
	)
	if err != nil {
		logger.Fatal("Failed to connect to hub at %s: %v", hubAddr, err)
	}
	defer conn.Close()

	client := augurv1.NewAgentPlaneServiceClient(conn)

	// Register with control plane
	regResp, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{
		AgentId:     agentID,
		AgentType:   agentType,
		Hostname:    hostname,
		WorkloadIds: workloadIDs,
	})
	if err != nil {
		logger.Fatal("Failed to register agent: %v", err)
	}
	if regResp.MetricsIntervalSec > 0 {
		intervalSec = int(regResp.MetricsIntervalSec)
	}
	logger.Info("Agent registered: id=%s, interval=%ds", agentID, intervalSec)

	// Start bidirectional metrics stream
	stream, err := client.MetricsStream(ctx)
	if err != nil {
		logger.Fatal("Failed to open metrics stream: %v", err)
	}

	// Receive scaling commands in background
	go func() {
		for {
			cmd, err := stream.Recv()
			if err != nil {
				logger.Error("Stream recv error: %v", err)
				return
			}
			handleScalingCommand(ctx, client, cmd)
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
				for _, wID := range workloadIDs {
					if wID == "" {
						continue
					}
					metrics := collectMetrics(wID)
					if err := stream.Send(metrics); err != nil {
						logger.Error("Failed to send metrics: %v", err)
					}
				}
			}
		}
	}()

	// Wait for signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("Received signal %s, shutting down edge agent...", sig)
}

// handleScalingCommand executes a scaling command from the control plane
func handleScalingCommand(ctx context.Context, client augurv1.AgentPlaneServiceClient, cmd *augurv1.ScalingCommand) {
	logger.Info("Received scaling command: workload=%s, action=%s, target=%d, reason=%s",
		cmd.WorkloadId, cmd.Action, cmd.TargetReplicas, cmd.Reasoning)

	if cmd.DryRun {
		logger.Info("Dry run — not executing")
		return
	}

	// Execute the scaling action based on agent type
	// In production, this would call the Firecracker API, K8s HPA, etc.
	success := executeScale(cmd)

	outcome := "healthy"
	errMsg := ""
	if !success {
		outcome = "failed"
		errMsg = "scaling action failed"
	}

	// Report outcome back to control plane
	_, err := client.ReportOutcome(ctx, &augurv1.ScalingOutcomeReport{
		CommandId:      cmd.CommandId,
		WorkloadId:     cmd.WorkloadId,
		Success:        success,
		Outcome:        outcome,
		ErrorMessage:   errMsg,
		ActualReplicas: cmd.TargetReplicas,
	})
	if err != nil {
		logger.Error("Failed to report outcome: %v", err)
	}
}

// collectMetrics collects current metrics for a workload
// In production, this reads from cgroups, /proc, K8s metrics API, etc.
func collectMetrics(workloadID string) *augurv1.AgentMetricsReport {
	return &augurv1.AgentMetricsReport{
		WorkloadId:          workloadID,
		CpuUtilizationPct:   readCPU(),
		MemoryUtilizationPct: readMemory(),
		RequestsPerSec:      readRequestRate(),
		LatencyP99Ms:        readLatency(),
		ErrorRatePct:        readErrorRate(),
		CurrentReplicas:     readReplicaCount(),
		AgentStatus:         "connected",
	}
}

// executeScale executes the actual scaling action
// Placeholder — in production, calls Firecracker API, K8s HPA, ASG, etc.
func executeScale(cmd *augurv1.ScalingCommand) bool {
	logger.Info("Executing scale: workload=%s → %d replicas", cmd.WorkloadId, cmd.TargetReplicas)
	// Simulate execution
	return true
}

// Metric collection stubs — in production, these read from the actual infrastructure
func readCPU() float64         { return 30 + rand.Float64()*40 }
func readMemory() float64      { return 40 + rand.Float64()*30 }
func readRequestRate() float64  { return 100 + rand.Float64()*500 }
func readLatency() float64     { return 50 + rand.Float64()*200 }
func readErrorRate() float64   { return rand.Float64() * 0.5 }
func readReplicaCount() int32  { return 3 }

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
