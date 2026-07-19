package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"

	"github.com/sentiae/platform-kit/interceptor"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// AgentPlaneServer is the hub's SECOND gRPC listener (P5b, D-177): a strict-mTLS
// endpoint that serves ONLY the AgentPlaneService. Its TLS config
// (VerifyClientCertIfGiven + agent-CA ClientCAs, from HubTLSProvider) lets the
// pre-cert Enroll RPC connect while cryptographically verifying any presented
// agent client cert at the handshake; the AgentPrincipalInterceptor then enforces
// per-method cert presence + cross-checks the enrolled agents row. The interceptor
// chain is recovery-FIRST (panic → Internal) then AgentPrincipal, so a panic never
// escapes and identity is derived before any handler runs.
type AgentPlaneServer struct {
	port       string
	grpcServer *grpc.Server
	healthSrv  *health.Server
	listener   net.Listener
}

// NewAgentPlaneServer builds the mTLS agent-plane server. tlsCfg comes from the
// HubTLSProvider (server cert + agent-CA pool); ap authenticates each call.
func NewAgentPlaneServer(port string, tlsCfg *tls.Config, agentServer *AgentServer, ap *AgentPrincipalInterceptor) *AgentPlaneServer {
	grpcSrv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(
			interceptor.UnaryRecovery(slog.Default()),
			ap.Unary(),
		),
		grpc.ChainStreamInterceptor(
			interceptor.StreamRecovery(slog.Default()),
			ap.Stream(),
		),
	)

	// ONLY the agent plane is served here — the control plane stays on the
	// plaintext listener so an agent's generated client cannot name it (D-177).
	augurv1.RegisterAgentPlaneServiceServer(grpcSrv, agentServer)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("augur.v1.AgentPlaneService", grpc_health_v1.HealthCheckResponse_SERVING)

	return &AgentPlaneServer{
		port:       port,
		grpcServer: grpcSrv,
		healthSrv:  healthSrv,
	}
}

// Addr returns the bound listener address (empty until Start binds).
func (s *AgentPlaneServer) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Start binds the mTLS listener and serves until GracefulStop is called or ctx is
// cancelled. Blocks — call in a goroutine.
func (s *AgentPlaneServer) Start(ctx context.Context) error {
	port := s.port
	if port == "" {
		port = "50060"
	}
	addr := fmt.Sprintf(":%s", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("agent-plane mTLS listen on %s failed: %w", addr, err)
	}
	s.listener = lis

	logger.Info("agent-plane mTLS gRPC server listening on %s", lis.Addr().String())

	go func() {
		<-ctx.Done()
		logger.Info("agent-plane server context cancelled, triggering graceful stop")
		s.healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		s.healthSrv.SetServingStatus("augur.v1.AgentPlaneService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		s.grpcServer.GracefulStop()
	}()

	if err := s.grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		return fmt.Errorf("agent-plane serve failed: %w", err)
	}
	return nil
}

// GracefulStop drains in-flight RPCs then stops. Safe to call multiple times.
func (s *AgentPlaneServer) GracefulStop() {
	if s.grpcServer == nil {
		return
	}
	s.healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.healthSrv.SetServingStatus("augur.v1.AgentPlaneService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.grpcServer.GracefulStop()
}
