package di

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	grpcHandler "github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc"
	"github.com/sentiae/infrastructure-intelligence-service/internal/handler/http"
	"github.com/sentiae/infrastructure-intelligence-service/internal/infrastructure/metrics"
	vaultgw "github.com/sentiae/infrastructure-intelligence-service/internal/infrastructure/vault"
	"github.com/sentiae/infrastructure-intelligence-service/internal/infrastructure/workclient"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/internal/usecase"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/config"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
	pkgmiddleware "github.com/sentiae/platform-kit/middleware"
	"github.com/sentiae/platform-kit/tenant"
	"github.com/sentiae/platform-kit/tenantdb"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Container holds all dependencies for the application
type Container struct {
	Config *config.Config
	DB     *gorm.DB

	// Repositories
	WorkloadRepo *postgres.WorkloadRepository
	DecisionRepo *postgres.DecisionRepository
	PolicyRepo   *postgres.PolicyRepository
	AlertRepo    *postgres.AlertRepository
	SLORepo      *postgres.SLORepository
	CostRepo     *postgres.CostRepository
	MetricsRepo  *postgres.MetricsRepository

	// TenantResolver resolves owning orgs via the D-072 SECURITY DEFINER rls_*
	// functions; used as the OrgLister for the cross-org loop sweeps (Model D) and
	// the by-id OrgResolver for the HTTP/gRPC handlers.
	TenantResolver *postgres.TenantResolverRepo

	// JWKSValidator validates BFF-forwarded user Bearer tokens for the HTTP auth
	// middleware (D-073). Nil when RLS enforcement is off and JWKS is unavailable.
	JWKSValidator pkgmiddleware.TokenValidator

	// Use Cases
	WorkloadService        *usecase.WorkloadService
	DecisionEngine         *usecase.DecisionEngine
	SLOEngine              *usecase.SLOEngine
	CostAnalyzer           *usecase.CostAnalyzer
	AnomalyDetector        *usecase.AnomalyDetector
	AlertService           *usecase.AlertService
	OutcomeObserver        *usecase.OutcomeObserver
	DeployObserver         *usecase.DeployObserver
	CostTracker            *usecase.CostTracker
	IdleDetector           *usecase.IdleDetector
	RightsizingEng         *usecase.RightsizingEngine
	SpotManager            *usecase.SpotManager
	PredictionEngine       *usecase.PredictionEngine
	CapacitySimulator      *usecase.CapacitySimulator
	MetricsCleaner         *usecase.MetricsCleaner
	MultiLayerDetector     *usecase.MultiLayerAnomalyDetector
	RIRecommender          *usecase.RIRecommender
	CrossClusterOptimizer  *usecase.CrossClusterOptimizer
	SpecCreator            *usecase.SpecCreator

	// HTTP Server
	HTTPServer *http.Server

	// gRPC server (edge-agent control plane — RegisterAgent, MetricsStream,
	// ReportOutcome, GetPolicy, SendScalingCommand). Nil when
	// config.Server.GRPC.Enabled is false.
	GRPCServer  *grpcHandler.Server
	AgentServer *grpcHandler.AgentServer

	// Event Publisher
	EventPublisher events.EventPublisher

	// Event Consumer
	OpsEventConsumer *events.KafkaConsumer

	// Infrastructure clients
	WorkClient      *workclient.Client
	MetricsClient   *metrics.VictoriaMetricsClient

	// PKIClient signs short-lived per-agent client certs (P3, D-177). Nil unless
	// the agent plane is enabled (APP_AGENT_PLANE_ENABLED); a build failure when
	// enabled is fatal (fail-closed) — see initAgentPlane.
	PKIClient *vaultgw.PKIClient
}

// NewContainer creates and initializes a new dependency injection container
func NewContainer(cfg *config.Config) (*Container, error) {
	c := &Container{Config: cfg}

	if err := c.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	if err := c.initAuth(); err != nil {
		return nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	if err := c.initAgentPlane(); err != nil {
		return nil, fmt.Errorf("failed to initialize agent plane: %w", err)
	}

	c.initInfrastructure()
	c.initRepositories()
	c.initUseCases()
	c.initConsumers()
	c.initHandlers()
	c.initGRPC()

	return c, nil
}

// initDatabase initializes the database connection and runs auto-migration
func (c *Container) initDatabase() error {
	port, err := strconv.Atoi(c.Config.Database.Postgres.Port)
	if err != nil {
		port = 5432
	}

	logLevel := gormlogger.Warn
	switch c.Config.Database.Postgres.LogLevel {
	case "info":
		logLevel = gormlogger.Info
	case "error":
		logLevel = gormlogger.Error
	case "silent":
		logLevel = gormlogger.Silent
	}

	pg := c.Config.Database.Postgres

	// OWNER connection for schema DDL (golang-migrate baseline incl. RLS) — D-070 role
	// split. Uses MigrateUser/MigratePassword when set, else falls back to the app
	// creds so an unsplit deploy connects as the same role as before. Short-lived
	// and closed immediately after schema setup so no DDL-capable pool lingers.
	ownerUser, ownerPassword := pg.User, pg.Password
	if pg.MigrateUser != "" {
		ownerUser, ownerPassword = pg.MigrateUser, pg.MigratePassword
	}
	ownerDB, err := postgres.NewDB(postgres.Config{
		Host:     pg.Host,
		Port:     port,
		User:     ownerUser,
		Password: ownerPassword,
		Database: pg.Database,
		SSLMode:  pg.SSLMode,
		LogLevel: logLevel,
	})
	if err != nil {
		return fmt.Errorf("open owner connection: %w", err)
	}

	// Schema substrate (CLAUDE.md §24): golang-migrate is the authoritative source
	// (D-178). RunMigrations applies the embedded baseline on the OWNER connection —
	// tables + indexes + tenant_isolation RLS + SECURITY DEFINER org resolvers all
	// live in migrations/0001_baseline.up.sql, replacing the old AutoMigrate +
	// ApplyRLSObjects boot path. Idempotent: an already-current DB is a no-op.
	version, applied, err := postgres.RunMigrations(ownerDB)
	if err != nil {
		closeDB(ownerDB)
		return fmt.Errorf("run migrations: %w", err)
	}
	logger.Info("Database migrations completed: schema_version=%d applied=%t", version, applied)
	closeDB(ownerDB)

	// APP pool (long-lived). Post-flip these are the NOBYPASSRLS app creds.
	db, err := postgres.NewDB(postgres.Config{
		Host:     pg.Host,
		Port:     port,
		User:     pg.User,
		Password: pg.Password,
		Database: pg.Database,
		SSLMode:  pg.SSLMode,
		LogLevel: logLevel,
	})
	if err != nil {
		return err
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(pg.Pool.MaxOpenConns)
	sqlDB.SetMaxIdleConns(pg.Pool.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(pg.Pool.MaxLifetime)
	sqlDB.SetConnMaxIdleTime(pg.Pool.MaxIdleTime)

	c.DB = db

	// P4 RLS read-path enforcement (D-071), flag-gated. Registering the Enforce
	// plugin auto-stamps every non-tx statement with the acting org (resolved from
	// the statement's ctx) so bare reads/writes are tenant-scoped; the boot posture
	// assertion then fails LOUD if enforcement is on while the app pool still
	// connects as a BYPASSRLS/superuser role (policies exist but every row is
	// visible — the silent-RLS-off footgun). Registration BEFORE assertion. Flag off
	// → not registered → behavior-neutral shadow.
	if config.RLSEnforceEnabled() {
		if err := db.Use(tenantdb.Enforce()); err != nil {
			return fmt.Errorf("register RLS enforce plugin: %w", err)
		}
		logger.Info("RLS Enforce plugin registered on app pool (read-path enforcement ON)")
		if err := tenantdb.AssertPosture(db, tenantdb.PostureEnforced); err != nil {
			return fmt.Errorf("RLS boot posture assertion failed: %w", err)
		}
		logger.Info("RLS boot posture verified (app role is RLS-enforced)")
	}

	logger.Info("Database connected and migrated")
	return nil
}

// closeDB closes the underlying *sql.DB of a gorm connection, ignoring errors —
// used to release the short-lived owner connection after schema setup.
func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// initAuth builds the JWKS-backed user-token validator used by the HTTP auth
// middleware (D-073), reusing the same JWKS URL + issuer the gRPC auth chain uses.
//
// Fail-boot posture: when RLS enforcement is ON — the live state — a service that
// silently disables auth reverts to the cross-tenant leak, so an empty
// APP_AUTH_JWKS_URL or a build failure is fatal. When enforcement is OFF (shadow)
// an unavailable validator degrades to nil (auth disabled, behavior-neutral).
func (c *Container) initAuth() error {
	jwks, err := tenant.NewJWKSValidator(tenant.JWKSConfig{
		JWKSURL: c.Config.Security.Auth.JWKSURL,
		Issuer:  c.Config.Security.Auth.JWTIssuer,
	})
	if config.RLSEnforceEnabled() {
		if c.Config.Security.Auth.JWKSURL == "" {
			return fmt.Errorf("RLS enforcement is on but APP_AUTH_JWKS_URL is empty: refusing to boot without user-JWT validation (D-073)")
		}
		if err != nil {
			return fmt.Errorf("RLS enforcement is on but building the JWKS validator failed: %w (D-073)", err)
		}
	} else if err != nil {
		logger.Warn("JWKS validator unavailable; HTTP JWT auth disabled (RLS enforcement off): %v", err)
		c.JWKSValidator = nil
		return nil
	}
	logger.Info("HTTP JWT auth enabled via JWKS (issuer: %s)", c.Config.Security.Auth.JWTIssuer)
	c.JWKSValidator = jwks
	return nil
}

// initAgentPlane builds the Vault-PKI client for the agent identity subsystem
// (P3, D-177), gated on APP_AGENT_PLANE_ENABLED.
//
// Fail-closed posture: when the agent plane is ENABLED, augur cannot sign the
// per-agent certs that authenticate the agent-plane mTLS listener without this
// client — a control that can't prove itself must not serve — so a build failure
// is FATAL. When DISABLED (the default until P5), no Vault client is built and
// augur boots exactly as before.
func (c *Container) initAgentPlane() error {
	if !c.Config.AgentPlane.Enabled {
		logger.Info("Agent plane disabled (set APP_AGENT_PLANE_ENABLED=true to enable)")
		return nil
	}
	pki, err := vaultgw.NewPKIClient(context.Background())
	if err != nil {
		return fmt.Errorf("agent plane enabled but building the Vault-PKI client failed: %w (D-177)", err)
	}
	c.PKIClient = pki
	logger.Info("Agent-plane Vault-PKI client wired (D-177)")
	return nil
}

// initInfrastructure initializes Kafka publisher and external clients
func (c *Container) initInfrastructure() {
	// Kafka publisher
	if c.Config.Messaging.Kafka.Enabled && c.Config.Features.EventPublishing {
		c.EventPublisher = events.NewKafkaPublisher(
			c.Config.GetKafkaBrokers(),
			c.Config.GetAugurEventsTopic(),
			true,
		)
		ensureCtx, ensureCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := c.EventPublisher.EnsureTopics(ensureCtx); err != nil {
			log.Printf("Warning: infrastructure-intelligence-service Kafka EnsureTopics failed: %v (continuing)", err)
		}
		ensureCancel()
	} else {
		c.EventPublisher = events.NewNoopPublisher()
	}

	// Work-service client (for spec auto-creation)
	if c.Config.Services.Work.Enabled {
		c.WorkClient = workclient.NewClient(
			c.Config.Services.Work.URL,
			c.Config.Services.Work.Timeout,
			c.Config.Services.Work.ServiceToken,
		)
		logger.Info("Work-service client initialized: %s", c.Config.Services.Work.URL)
	}

	// VictoriaMetrics client (for metrics push)
	if c.Config.Observability.VictoriaMetrics.Enabled {
		vmCfg := c.Config.Observability.VictoriaMetrics
		c.MetricsClient = metrics.NewVictoriaMetricsClient(metrics.Config{
			URL:       vmCfg.URL,
			AuthToken: vmCfg.AuthToken,
			Timeout:   vmCfg.Timeout,
			FlushSize: vmCfg.FlushSize,
		})
		logger.Info("VictoriaMetrics client initialized: %s", vmCfg.URL)
	}
}

// initRepositories creates all repository instances
func (c *Container) initRepositories() {
	c.WorkloadRepo = postgres.NewWorkloadRepository(c.DB)
	c.DecisionRepo = postgres.NewDecisionRepository(c.DB)
	c.PolicyRepo = postgres.NewPolicyRepository(c.DB)
	c.AlertRepo = postgres.NewAlertRepository(c.DB)
	c.SLORepo = postgres.NewSLORepository(c.DB)
	c.CostRepo = postgres.NewCostRepository(c.DB)
	c.MetricsRepo = postgres.NewMetricsRepository(c.DB)

	// Org resolver (D-072) — built on the app pool; the OrgLister for the loop
	// sweeps and the OrgResolver for the HTTP/gRPC by-id handlers. Constructed
	// BEFORE initUseCases so the loop constructors receive it.
	c.TenantResolver = postgres.NewTenantResolverRepo(c.DB)
}

// initUseCases creates all use case instances with injected dependencies
func (c *Container) initUseCases() {
	c.WorkloadService = usecase.NewWorkloadService(
		c.WorkloadRepo,
		c.MetricsRepo,
		c.PolicyRepo,
		c.EventPublisher,
		c.Config.Engine.ObservationPeriodDays,
	)

	c.DecisionEngine = usecase.NewDecisionEngine(
		c.WorkloadRepo,
		c.DecisionRepo,
		c.PolicyRepo,
		c.AlertRepo,
		c.EventPublisher,
		c.TenantResolver,
		c.Config.Engine.DecisionIntervalSec,
		c.Config.Engine.MaxActionsPerHour,
		c.Config.Engine.CooldownScaleUp,
		c.Config.Engine.CooldownScaleDown,
		c.Config.Engine.CircuitBreakerThreshold,
		c.Config.Engine.RollbackWindowMin,
	)

	c.SLOEngine = usecase.NewSLOEngine(
		c.SLORepo,
		c.WorkloadRepo,
		c.MetricsRepo,
		c.AlertRepo,
		c.EventPublisher,
	)

	c.CostAnalyzer = usecase.NewCostAnalyzer(
		c.CostRepo,
		c.WorkloadRepo,
		c.EventPublisher,
	)

	c.AnomalyDetector = usecase.NewAnomalyDetector(
		c.MetricsRepo,
		c.WorkloadRepo,
		c.AlertRepo,
		c.EventPublisher,
	)

	c.AlertService = usecase.NewAlertService(c.AlertRepo)

	c.OutcomeObserver = usecase.NewOutcomeObserver(
		c.DecisionRepo,
		c.WorkloadRepo,
		c.MetricsRepo,
		c.EventPublisher,
		c.TenantResolver,
		c.Config.Engine.RollbackWindowMin,
	)

	c.DeployObserver = usecase.NewDeployObserver(
		c.WorkloadRepo,
		c.Config.Engine.PostDeployObserveMin,
	)

	c.CostTracker = usecase.NewCostTracker(
		c.CostRepo,
		c.WorkloadRepo,
		c.EventPublisher,
		c.TenantResolver,
	)

	c.IdleDetector = usecase.NewIdleDetector(
		c.WorkloadRepo,
		c.MetricsRepo,
		c.CostRepo,
		c.EventPublisher,
		c.TenantResolver,
	)

	c.RightsizingEng = usecase.NewRightsizingEngine(
		c.WorkloadRepo,
		c.MetricsRepo,
	)

	c.SpotManager = usecase.NewSpotManager(
		c.WorkloadRepo,
		c.EventPublisher,
	)

	c.PredictionEngine = usecase.NewPredictionEngine(
		c.WorkloadRepo,
		c.MetricsRepo,
		c.EventPublisher,
		c.TenantResolver,
	)

	// Wire prediction engine into decision engine for predictive scaling
	c.DecisionEngine.SetPredictionEngine(c.PredictionEngine)

	c.CapacitySimulator = usecase.NewCapacitySimulator(
		c.WorkloadRepo,
		c.PredictionEngine,
	)

	c.MetricsCleaner = usecase.NewMetricsCleaner(c.MetricsRepo, c.TenantResolver, 7)

	// Phase 4: Multi-environment intelligence
	c.MultiLayerDetector = usecase.NewMultiLayerAnomalyDetector(
		c.MetricsRepo,
		c.WorkloadRepo,
	)

	c.RIRecommender = usecase.NewRIRecommender(
		c.WorkloadRepo,
		c.MetricsRepo,
	)

	c.CrossClusterOptimizer = usecase.NewCrossClusterOptimizer(c.WorkloadRepo)

	// Spec auto-creation (bridges Augur events → work-service specs)
	c.SpecCreator = usecase.NewSpecCreator(
		c.WorkClient,
		c.WorkloadRepo,
		c.Config.Features.SpecAutoCreation,
	)
}

// initConsumers sets up Kafka event consumers
func (c *Container) initConsumers() {
	if !c.Config.Messaging.Kafka.Enabled {
		return
	}

	consumer, err := events.NewKafkaConsumer(
		c.Config.GetKafkaBrokers(),
		c.Config.Messaging.Kafka.GroupID,
		[]string{c.Config.GetOpsEventsTopic()},
		c.handleOpsEvent,
	)
	if err != nil {
		logger.Error("Failed to create ops event consumer: %v (continuing without consumer)", err)
		return
	}
	c.OpsEventConsumer = consumer
}

// initHandlers creates HTTP handlers
func (c *Container) initHandlers() {
	c.HTTPServer = http.NewServer(
		c.JWKSValidator,
		c.Config.Security.Auth.ServiceAPIKey,
		c.TenantResolver,
		c.WorkloadService,
		c.DecisionEngine,
		c.SLOEngine,
		c.CostAnalyzer,
		c.AnomalyDetector,
		c.AlertService,
		c.RightsizingEng,
		c.SpotManager,
		c.PredictionEngine,
		c.CapacitySimulator,
		c.MultiLayerDetector,
		c.RIRecommender,
		c.CrossClusterOptimizer,
	)
}

// initGRPC wires the edge-agent gRPC server. The AgentServer is always
// constructed so tests can drive it, but the network listener only starts when
// config.Server.GRPC.Enabled is true — controlled by StartGRPC in main.go.
func (c *Container) initGRPC() {
	c.AgentServer = grpcHandler.NewAgentServer(
		c.WorkloadRepo,
		c.PolicyRepo,
		c.DecisionRepo,
		c.WorkloadService,
		c.DecisionEngine,
		c.TenantResolver,
	)

	if !c.Config.Server.GRPC.Enabled {
		logger.Info("gRPC server disabled (set server.grpc.enabled=true to enable)")
		return
	}

	c.GRPCServer = grpcHandler.NewServer(grpcHandler.ServerConfig{
		Host:          c.Config.Server.GRPC.Host,
		Port:          c.Config.Server.GRPC.Port,
		ServiceAPIKey: c.Config.Security.Auth.ServiceAPIKey,
		JWKSURL:       c.Config.Security.Auth.JWKSURL,
		JWTIssuer:     c.Config.Security.Auth.JWTIssuer,
	}, c.AgentServer)
}

// StartGRPC starts the edge-agent gRPC server. Call in a goroutine — Start
// blocks until the context is cancelled or an error occurs.
func (c *Container) StartGRPC(ctx context.Context) error {
	if c.GRPCServer == nil {
		logger.Info("gRPC server not configured — skipping")
		return nil
	}
	return c.GRPCServer.Start(ctx)
}

// StartConsumers begins consuming Kafka events. Call in a goroutine.
func (c *Container) StartConsumers(ctx context.Context) {
	if c.OpsEventConsumer == nil {
		logger.Info("No Kafka consumers configured — skipping")
		return
	}

	logger.Info("Starting Kafka consumers...")
	if err := c.OpsEventConsumer.Start(ctx); err != nil {
		logger.Error("Ops event consumer error: %v", err)
	}
}

// StartDecisionEngine starts the autonomous decision loop. Call in a goroutine.
func (c *Container) StartDecisionEngine(ctx context.Context) {
	c.DecisionEngine.Run(ctx)
}

// StartOutcomeObserver starts the outcome observer loop. Call in a goroutine.
func (c *Container) StartOutcomeObserver(ctx context.Context) {
	c.OutcomeObserver.Run(ctx)
}

// StartCostTracker starts the continuous cost tracking loop. Call in a goroutine.
func (c *Container) StartCostTracker(ctx context.Context) {
	c.CostTracker.Run(ctx)
}

// StartIdleDetector starts the idle resource detection loop. Call in a goroutine.
func (c *Container) StartIdleDetector(ctx context.Context) {
	c.IdleDetector.Run(ctx)
}

// StartPredictionEngine starts the ML prediction engine loop. Call in a goroutine.
func (c *Container) StartPredictionEngine(ctx context.Context) {
	if c.Config.Features.MLPrediction {
		c.PredictionEngine.Run(ctx)
	} else {
		logger.Info("ML prediction engine disabled (set features.ml_prediction=true to enable)")
	}
}

// handleOpsEvent handles incoming events from the ops-service topic
func (c *Container) handleOpsEvent(ctx context.Context, event events.CloudEvent) error {
	logger.Debug("Received ops event: type=%s, id=%s", event.Type, event.ID)

	switch event.Type {
	case "sentiae.ops.deploy.completed":
		return c.handleDeployCompleted(ctx, event)
	case "sentiae.ops.alert.fired":
		return c.handleAlertFired(ctx, event)
	default:
		logger.Debug("Ignoring unhandled ops event type: %s", event.Type)
	}
	return nil
}

// handleDeployCompleted puts affected workloads in post-deploy observation mode
func (c *Container) handleDeployCompleted(ctx context.Context, event events.CloudEvent) error {
	var data map[string]interface{}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return fmt.Errorf("failed to unmarshal event data: %w", err)
	}

	envID, _ := data["environment_id"].(string)
	if envID == "" {
		return nil
	}

	logger.Info("Deploy completed for environment %s — entering post-deploy observation mode", envID)

	// Model D (D-071): the event carries only environment_id, no org. When RLS
	// enforcement is ON the app pool is NOBYPASSRLS so a bare sweep sees zero rows;
	// ForEachOrg re-stamps per org (tenant.WithSystemOrg) so each org's workloads
	// are observed under its own scope. When enforcement is OFF it runs once on the
	// original ctx — byte-identical legacy behavior. A single org's failure is
	// logged and skipped inside ForEachOrg, never aborting the sweep.
	return usecase.ForEachOrg(ctx, c.TenantResolver, func(orgCtx context.Context) error {
		return c.DeployObserver.OnDeployCompleted(orgCtx, envID)
	})
}

// handleAlertFired handles monitor node alerts from ops-service
func (c *Container) handleAlertFired(ctx context.Context, event events.CloudEvent) error {
	logger.Info("Monitor alert fired — evaluating scaling response")
	return nil
}

// Close cleans up all resources
func (c *Container) Close() {
	if c.PKIClient != nil {
		if err := c.PKIClient.Close(); err != nil {
			logger.Warn("PKI client close failed: %v", err)
		}
	}
	if c.MetricsClient != nil {
		c.MetricsClient.Close()
	}
	if c.EventPublisher != nil {
		c.EventPublisher.Close()
	}
	if c.OpsEventConsumer != nil {
		c.OpsEventConsumer.Close()
	}
	if c.DB != nil {
		sqlDB, err := c.DB.DB()
		if err == nil {
			sqlDB.Close()
		}
	}
	logger.Info("Container resources closed")
}
