package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/internal/di"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/config"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

func main() {
	// Initialize logger
	logger.Init("info")
	logger.Info("Starting infrastructure-intelligence-service (Augur)...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration: %v", err)
	}
	logger.Info("Configuration loaded: env=%s, http_port=%s", cfg.App.Environment, cfg.Server.HTTP.Port)

	// Initialize DI container
	container, err := di.NewContainer(cfg)
	if err != nil {
		logger.Fatal("Failed to initialize container: %v", err)
	}
	defer container.Close()

	// Start HTTP server
	httpAddr := fmt.Sprintf("%s:%s", cfg.Server.HTTP.Host, cfg.Server.HTTP.Port)
	httpServer := &http.Server{
		Addr:         httpAddr,
		Handler:      container.HTTPServer,
		ReadTimeout:  cfg.Server.HTTP.Timeouts.Read,
		WriteTimeout: cfg.Server.HTTP.Timeouts.Write,
		IdleTimeout:  cfg.Server.HTTP.Timeouts.Idle,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("HTTP server listening on %s", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP server failed: %v", err)
		}
	}()

	// Start gRPC server (edge-agent control plane) in a goroutine. The
	// server wrapper binds to cfg.Server.GRPC.Host:Port, registers the
	// AgentPlaneService + ControlPlaneService, the standard grpc.health.v1
	// health service, and reflection. It respects ctx.Done() for graceful
	// shutdown.
	grpcCtx, grpcCancel := context.WithCancel(context.Background())
	defer grpcCancel()
	go func() {
		if err := container.StartGRPC(grpcCtx); err != nil {
			logger.Error("gRPC server error: %v", err)
		}
	}()

	// Start the agent-plane mTLS listener (P5b, D-177) in a goroutine. No-op
	// unless the agent plane is enabled. Shares grpcCtx so it stops with the rest
	// of the gRPC surface on shutdown.
	go func() {
		if err := container.StartAgentPlane(grpcCtx); err != nil {
			logger.Error("agent-plane mTLS server error: %v", err)
		}
	}()

	// Start Kafka event consumers
	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	go container.StartConsumers(consumerCtx)

	// Start the Decision Engine loop
	engineCtx, engineCancel := context.WithCancel(context.Background())
	defer engineCancel()
	go container.StartDecisionEngine(engineCtx)

	// Start the Outcome Observer (checks scaling decision outcomes after rollback window)
	go container.StartOutcomeObserver(engineCtx)

	// Start the Cost Tracker (continuous cost tracking and budget enforcement)
	go container.StartCostTracker(engineCtx)

	// Start the Idle Detector (scans for idle resources hourly)
	go container.StartIdleDetector(engineCtx)

	// Start the ML Prediction Engine (forecasts every 15 minutes)
	go container.StartPredictionEngine(engineCtx)

	// Start the Metrics Cleaner (removes old snapshots every 6 hours)
	go container.MetricsCleaner.Run(engineCtx)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info("Received signal %s, shutting down...", sig)

	// Graceful shutdown
	shutdownTimeout := cfg.Server.HTTP.Timeouts.Shutdown
	if shutdownTimeout == 0 {
		shutdownTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("HTTP server shutdown error: %v", err)
	}

	// Gracefully stop the gRPC server — drains in-flight RPCs and then
	// returns from Serve. Cancelling grpcCtx is enough (the Start goroutine
	// reacts to ctx.Done() by invoking GracefulStop), but we call
	// GracefulStop directly so shutdown order is explicit.
	if container.GRPCServer != nil {
		logger.Info("Gracefully stopping gRPC server...")
		container.GRPCServer.GracefulStop()
	}
	if container.AgentPlaneServer != nil {
		logger.Info("Gracefully stopping agent-plane mTLS server...")
		container.AgentPlaneServer.GracefulStop()
	}
	grpcCancel()

	logger.Info("infrastructure-intelligence-service stopped")
}
