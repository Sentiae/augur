package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	pkconfig "github.com/sentiae/platform-kit/config"
	"github.com/sentiae/platform-kit/grpcserver"
	"github.com/sentiae/platform-kit/interceptor"
	"github.com/sentiae/platform-kit/spiffe"
	"github.com/sentiae/platform-kit/tenant"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// controlPlaneAuthzMethods are the ControlPlaneService RPCs guarded by per-method
// SVID service-authz on the mesh listener (P7a, D-179). A caller presenting a peer
// SVID with no matching grant is denied; the ops→augur grant itself is DEFERRED to
// P7b (no live caller yet), so these methods fail closed until then. All four
// service-to-service ControlPlane RPCs are now guarded: DispatchDeploy/GetAgentStatus
// (the deploy actuation ops drives) plus the P4 agent-identity RPCs
// PreRegisterAgent/RevokeAgent (P7b, #augur-p7b-controlplane-client) — these mint
// enrollment tokens / revoke agent identities, so they are grant-gated (deny-all
// until a narrow ops-only grant lands) rather than open to any mesh-TCB peer SVID.
var controlPlaneAuthzMethods = map[string]struct{}{
	"/augur.v1.ControlPlaneService/DispatchDeploy":   {},
	"/augur.v1.ControlPlaneService/GetAgentStatus":   {},
	"/augur.v1.ControlPlaneService/PreRegisterAgent": {},
	"/augur.v1.ControlPlaneService/RevokeAgent":      {},
}

// ServerConfig holds runtime options for the gRPC server wrapper.
type ServerConfig struct {
	Host string
	Port string

	// gRPC auth interceptor (platform-kit tenant): ServiceAPIKey is the shared
	// x-api-key secret validated against inbound service callers. User JWTs never
	// ride the gRPC control plane — it is service-to-service — so no JWKS validator
	// is wired here; the mesh authenticates callers via their peer SVID.
	ServiceAPIKey string

	// ControlPlaneOnly registers ONLY the ControlPlaneService on this listener.
	// Set true by DI when the agent plane is enabled — the AgentPlane then lives
	// exclusively on the mTLS Vault-PKI listener (D-177). Default false keeps BOTH
	// services here (byte-identical to today when the plane is disabled).
	ControlPlaneOnly bool
}

// Server wraps the platform-kit mesh gRPC builder (grpcserver, D-179 SPIRE-SVID
// mesh) together with the application's business gRPC handlers and the standard
// health service. It owns the listener lifecycle — Start binds + serves until
// GracefulStop is invoked. The mesh transport is governed by APP_GRPC_MTLS_MODE
// (off | permissive | strict); the SPIFFE X509 source is built at Start.
type Server struct {
	cfg         ServerConfig
	healthSrv   *health.Server
	agentServer *AgentServer

	// Interceptor chains, precomputed in NewServer (pure) and handed to
	// grpcserver.New at Start (which needs a ctx to build the SVID source).
	unaryInts  []grpc.UnaryServerInterceptor
	streamInts []grpc.StreamServerInterceptor

	// mu guards builder + listener, which are constructed inside Start (a
	// goroutine) yet read by GracefulStop/Stop/Addr from other goroutines.
	mu       sync.Mutex
	builder  *grpcserver.Builder
	listener net.Listener
}

// NewServer constructs a Server wrapping the provided AgentServer. Callers are
// expected to start it via Start and tear it down via GracefulStop.
func NewServer(cfg ServerConfig, agentServer *AgentServer) *Server {
	// Mandatory server interceptor chain (CLAUDE.md §23): Recovery → Logging →
	// SVID → Auth, built by interceptor.NewChain. Auth is api-key only — the
	// control plane is service-to-service, so no user-JWT (JWKS) validator is
	// wired; the mesh derives the trusted caller from the peer SVID. Health +
	// reflection are skipped so k8s probes and grpcurl keep working
	// unauthenticated. Both unary and stream chains are installed — the stream
	// chain covers the bidirectional MetricsStream RPC (served here only when the
	// agent plane is disabled).
	svcToken := tenant.ServiceTokenValidator{Expected: cfg.ServiceAPIKey}
	unary, stream := interceptor.NewChain(interceptor.Config{
		Logger: slog.Default(),
		Auth: &interceptor.AuthConfig{
			APIKeyValidator: svcToken,
			AcceptAPIKey:    pkconfig.AcceptAPIKey(),
			RequirePeerSVID: pkconfig.RequirePeerSVID(),
			SkipMethods: []string{
				"/grpc.health.v1.Health/Check",
				"/grpc.health.v1.Health/Watch",
				"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
				"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
			},
		},
	})

	// SVID-authz mesh policy (T-SEC-FND Wave 4 / P7a): SetServiceGrants installs the
	// fleet cross-org policy consulted by CanActInOrg (the cross-org data-plane auth).
	tenant.SetServiceGrants(tenant.LoadMeshPolicy())
	tenant.SetMeshSVIDAuthzStrict(pkconfig.MeshSVIDAuthzStrict())

	// ⚠ The ControlPlane's per-method authz is a SEPARATE, DEDICATED grant set —
	// NOT LoadMeshPolicy(). The fleet policy grants every cross-org TCB SVID an
	// EMPTY-Methods grant, which tenant.AllowsMethod treats as allow-ALL-methods
	// (grants.go:68) — so passing it here would authorize all 10 TCB SVIDs on
	// DispatchDeploy/GetAgentStatus, the opposite of least privilege. Instead we
	// pass an EMPTY grant set: every caller is "unknown" → AllowsMethod denies →
	// the ControlPlane's DispatchDeploy/GetAgentStatus are DENY-ALL, genuinely
	// fail-closed. P7b replaces this with a narrow ops-only, method-scoped grant
	// (NewServiceGrants{ops: {Methods: {DispatchDeploy, GetAgentStatus}}}).
	controlPlaneGrants := tenant.NewServiceGrants(nil)

	// D-072 RLS org-field stamping + P7a service-authz: appended AFTER the auth
	// chain so the principal (peer SVID) is established first. UnaryOrgField
	// reflects a request's organization_id onto ctx (tenantdb stamps the tenant
	// GUC for RLS-scoped reads); UnaryServiceAuthz then enforces per-method grants.
	// StreamOrgField is registration symmetry only — the MetricsStream org is
	// handler-resolved per message.
	unary = append(unary, tenant.UnaryOrgField(), tenant.UnaryServiceAuthz(controlPlaneAuthzMethods, controlPlaneGrants))
	stream = append(stream, tenant.StreamOrgField())

	// Health service — follows the identity-service golden reference pattern:
	// register an empty service name for overall health plus the fully qualified
	// service names so Kubernetes probes and grpc.health.v1.Health clients can
	// distinguish "service healthy" from "process healthy". Registered on the mesh
	// builder at Start; created here so shutdown can flip serving status.
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("augur.v1.AgentPlaneService", grpc_health_v1.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("augur.v1.ControlPlaneService", grpc_health_v1.HealthCheckResponse_SERVING)

	return &Server{
		cfg:         cfg,
		healthSrv:   healthSrv,
		agentServer: agentServer,
		unaryInts:   unary,
		streamInts:  stream,
	}
}

// Addr returns the actual network address the listener is bound to. Useful in
// tests where port 0 is requested so the kernel can pick a free port.
func (s *Server) Addr() string {
	s.mu.Lock()
	lis := s.listener
	s.mu.Unlock()
	if lis == nil {
		return ""
	}
	return lis.Addr().String()
}

// AgentServer returns the underlying agent server for tests / back-channel use.
func (s *Server) AgentServer() *AgentServer {
	return s.agentServer
}

// Start builds the mesh gRPC server, binds the listener, and serves until
// GracefulStop is called or the context is cancelled. It blocks, so callers
// should invoke it in a goroutine.
func (s *Server) Start(ctx context.Context) error {
	// Build the SPIFFE X509 source when a mesh mode is configured. Degrade to nil
	// only when the source itself is unavailable: grpcserver.New then fail-closes
	// on strict+nil (refuses to serve) and escape-hatches to plaintext on
	// permissive+nil (a SPIRE hiccup must not wedge a service that opted into it).
	// APP_GRPC_MTLS_MODE=off keeps today's single plaintext listener.
	var source *workloadapi.X509Source
	if pkconfig.MTLSMode() != pkconfig.MTLSModeOff {
		xs, err := spiffe.NewSource(ctx)
		if err != nil {
			logger.Warn("SPIFFE source unavailable, degrading per mTLS mode %q: %v", pkconfig.MTLSMode(), err)
		} else {
			source = xs
		}
	}

	builder := grpcserver.New(grpcserver.Config{
		Mode:        pkconfig.MTLSMode(),
		Source:      source,
		ServiceName: "augur",
	},
		grpc.ChainUnaryInterceptor(s.unaryInts...),
		grpc.ChainStreamInterceptor(s.streamInts...),
	)

	// Business service. When the agent plane is enabled only the control plane is
	// served here; the agent plane lives on the mTLS Vault-PKI listener (D-177).
	// Otherwise both register here (single-listener behavior when the plane is
	// disabled). The mesh registrar fans registration to both transports.
	if s.cfg.ControlPlaneOnly {
		s.agentServer.RegisterControlPlaneOnly(builder.Registrar())
	} else {
		s.agentServer.RegisterServer(builder.Registrar())
	}
	grpc_health_v1.RegisterHealthServer(builder.Registrar(), s.healthSrv)
	// Reflection is auto-registered by grpcserver.Serve — do not register it here
	// (double registration on a *grpc.Server panics).

	host := s.cfg.Host
	port := s.cfg.Port
	if port == "" {
		port = "50059"
	}

	addr := fmt.Sprintf("%s:%s", host, port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		if source != nil {
			_ = source.Close()
		}
		return fmt.Errorf("grpc listen on %s failed: %w", addr, err)
	}

	s.mu.Lock()
	s.builder = builder
	s.listener = lis
	s.mu.Unlock()

	logger.Info("gRPC mesh server listening on %s (mTLS mode: %s)", lis.Addr().String(), pkconfig.MTLSMode())

	// Respect context cancellation for graceful shutdown.
	go func() {
		<-ctx.Done()
		logger.Info("gRPC server context cancelled, triggering graceful stop")
		s.GracefulStop()
	}()

	// Serve blocks until the listener is closed. A poisoned builder (strict mTLS
	// with no SVID source) returns its build error here without serving anything.
	serveErr := builder.Serve(lis)
	if source != nil {
		_ = source.Close()
	}
	if serveErr != nil && serveErr != grpc.ErrServerStopped {
		return fmt.Errorf("grpc serve failed: %w", serveErr)
	}
	return nil
}

// GracefulStop stops the underlying gRPC server, allowing in-flight RPCs to
// complete. Safe to call multiple times / before Start.
func (s *Server) GracefulStop() {
	s.mu.Lock()
	b := s.builder
	s.mu.Unlock()
	if b == nil {
		return
	}
	s.setNotServing()
	b.GracefulStop()
}

// Stop immediately aborts the gRPC server. Prefer GracefulStop in production.
func (s *Server) Stop() {
	s.mu.Lock()
	b := s.builder
	s.mu.Unlock()
	if b == nil {
		return
	}
	b.Stop()
}

// setNotServing flips every advertised health status to NOT_SERVING so probes
// see the shutdown before the listener closes.
func (s *Server) setNotServing() {
	s.healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.healthSrv.SetServingStatus("augur.v1.AgentPlaneService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.healthSrv.SetServingStatus("augur.v1.ControlPlaneService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
}
