package grpc

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// ServerConfig holds runtime options for the gRPC server wrapper.
type ServerConfig struct {
	Host string
	Port string
}

// Server wraps a google.golang.org/grpc server together with the application's
// business gRPC handlers and the standard health service. It owns the listener
// lifecycle — Start serves until GracefulStop is invoked.
type Server struct {
	cfg         ServerConfig
	grpcServer  *grpc.Server
	healthSrv   *health.Server
	agentServer *AgentServer
	listener    net.Listener
}

// NewServer constructs a Server wrapping the provided AgentServer. Callers are
// expected to start it via Start and tear it down via GracefulStop.
func NewServer(cfg ServerConfig, agentServer *AgentServer) *Server {
	grpcSrv := grpc.NewServer()

	// Business service
	agentServer.RegisterServer(grpcSrv)

	// Health service — follows the identity-service golden reference pattern:
	// register an empty service name for overall health plus the fully
	// qualified service name so Kubernetes probes and clients using the
	// grpc.health.v1.Health API can distinguish "service healthy" from
	// "process healthy".
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("augur.v1.AugurAgentService", grpc_health_v1.HealthCheckResponse_SERVING)

	// Reflection aids kubectl port-forward + grpcurl debugging in dev.
	reflection.Register(grpcSrv)

	return &Server{
		cfg:         cfg,
		grpcServer:  grpcSrv,
		healthSrv:   healthSrv,
		agentServer: agentServer,
	}
}

// Addr returns the actual network address the listener is bound to. Useful in
// tests where port 0 is requested so the kernel can pick a free port.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// AgentServer returns the underlying agent server for tests / back-channel use.
func (s *Server) AgentServer() *AgentServer {
	return s.agentServer
}

// Start binds the listener and serves until GracefulStop is called or the
// context is cancelled. It blocks, so callers should invoke it in a goroutine.
func (s *Server) Start(ctx context.Context) error {
	host := s.cfg.Host
	port := s.cfg.Port
	if port == "" {
		port = "50059"
	}

	addr := fmt.Sprintf("%s:%s", host, port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen on %s failed: %w", addr, err)
	}
	s.listener = lis

	logger.Info("gRPC server listening on %s", lis.Addr().String())

	// Respect context cancellation for graceful shutdown.
	go func() {
		<-ctx.Done()
		logger.Info("gRPC server context cancelled, triggering graceful stop")
		s.healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		s.healthSrv.SetServingStatus("augur.v1.AugurAgentService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		s.grpcServer.GracefulStop()
	}()

	if err := s.grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		return fmt.Errorf("grpc serve failed: %w", err)
	}
	return nil
}

// GracefulStop stops the underlying gRPC server, allowing in-flight RPCs to
// complete. Safe to call multiple times.
func (s *Server) GracefulStop() {
	if s.grpcServer == nil {
		return
	}
	s.healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.healthSrv.SetServingStatus("augur.v1.AugurAgentService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.grpcServer.GracefulStop()
}

// Stop immediately aborts the gRPC server. Prefer GracefulStop in production.
func (s *Server) Stop() {
	if s.grpcServer == nil {
		return
	}
	s.grpcServer.Stop()
}
