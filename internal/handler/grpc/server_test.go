package grpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	augurv1 "github.com/sentiae/infrastructure-intelligence-service/gen/proto/augur/v1"
	internalgrpc "github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc"
)

// newTestServer spins up a Server bound to an OS-picked port on localhost and
// returns the server plus its listener address. The AgentServer dependencies
// (repositories, use cases) are passed as nil because RegisterAgent — the only
// RPC exercised in these unit tests — never touches them.
func newTestServer(t *testing.T) (*internalgrpc.Server, string) {
	t.Helper()

	agentSrv := internalgrpc.NewAgentServer(nil, nil, nil, nil, nil, nil, nil, nil)
	srv := internalgrpc.NewServer(internalgrpc.ServerConfig{
		Host:          "127.0.0.1",
		Port:          "0", // kernel-picked
		ServiceAPIKey: testServiceAPIKey,
	}, agentSrv)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{})
	go func() {
		// Signal after the listener is bound. Start binds before serving, so
		// a brief yield plus polling Addr() gives tests a deterministic wait.
		close(started)
		_ = srv.Start(ctx)
	}()

	// Wait for the listener to bind. Start allocates the listener before
	// calling Serve, but does so inside the goroutine, so poll Addr() with
	// a short deadline instead of a hard sleep.
	<-started
	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Addr() == "" {
		t.Fatalf("grpc server did not bind within 2s")
	}

	return srv, srv.Addr()
}

// testServiceAPIKey is the shared service token the test server validates and
// the test client presents as x-api-key (service-principal path).
const testServiceAPIKey = "test-service-key"

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	// Present a valid service credential on every unary call so the mandatory
	// auth interceptor authenticates the caller as a trusted service.
	injectAuth := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", testServiceAPIKey, "x-service-name", "augur-test")
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(injectAuth),
	)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestServer_RegisterAgent(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := augurv1.NewAgentPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{
		AgentId:   "test-agent-1",
		AgentType: "vm",
		Hostname:  "test-host",
	})
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true, got %+v", resp)
	}
	if resp.MetricsIntervalSec == 0 {
		t.Fatalf("expected non-zero metrics interval")
	}
	if resp.PolicyVersion == "" {
		t.Fatalf("expected policy version")
	}

	if got := srv.AgentServer().ConnectedAgentCount(); got != 1 {
		t.Fatalf("expected 1 connected agent, got %d", got)
	}
}

func TestServer_RegisterAgent_RejectsEmptyID(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := augurv1.NewAgentPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{})
	if err == nil {
		t.Fatalf("expected error for empty agent_id, got nil")
	}
}

func TestServer_HealthCheck(t *testing.T) {
	srv, addr := newTestServer(t)
	t.Cleanup(srv.GracefulStop)

	conn := dial(t, addr)
	client := grpc_health_v1.NewHealthClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Overall process health.
	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health.Check: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("expected SERVING, got %s", resp.Status)
	}

	// Fully-qualified service name health — both split services (D-177) are
	// registered on the single listener and report SERVING.
	for _, svc := range []string{"augur.v1.AgentPlaneService", "augur.v1.ControlPlaneService"} {
		svcResp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{
			Service: svc,
		})
		if err != nil {
			t.Fatalf("Health.Check(%s): %v", svc, err)
		}
		if svcResp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Fatalf("expected SERVING for %s, got %s", svc, svcResp.Status)
		}
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	srv, addr := newTestServer(t)

	// Hold an open connection so we can confirm GracefulStop drains cleanly.
	conn := dial(t, addr)
	client := augurv1.NewAgentPlaneServiceClient(conn)

	// Warm up — confirms the server is accepting RPCs.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err := client.RegisterAgent(ctx, &augurv1.RegisterAgentRequest{AgentId: "warmup"})
	cancel()
	if err != nil {
		t.Fatalf("warmup RegisterAgent: %v", err)
	}

	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatalf("GracefulStop did not return within 5s")
	}

	// Post-shutdown RPCs must fail — grpc.NewClient keeps a connection open
	// but the listener is gone, so Invoke returns Unavailable.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel2()
	_, err = client.RegisterAgent(ctx2, &augurv1.RegisterAgentRequest{AgentId: "post-shutdown"})
	if err == nil {
		t.Fatalf("expected RPC after shutdown to fail")
	}
}
