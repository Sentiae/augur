package di

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	grpcHandler "github.com/sentiae/infrastructure-intelligence-service/internal/handler/grpc"
	"github.com/sentiae/infrastructure-intelligence-service/internal/handler/http"
	"github.com/sentiae/infrastructure-intelligence-service/internal/infrastructure/metrics"
	"github.com/sentiae/infrastructure-intelligence-service/internal/infrastructure/workclient"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/internal/usecase"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/config"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
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
}

// NewContainer creates and initializes a new dependency injection container
func NewContainer(cfg *config.Config) (*Container, error) {
	c := &Container{Config: cfg}

	if err := c.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
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

	dbCfg := postgres.Config{
		Host:     c.Config.Database.Postgres.Host,
		Port:     port,
		User:     c.Config.Database.Postgres.User,
		Password: c.Config.Database.Postgres.Password,
		Database: c.Config.Database.Postgres.Database,
		SSLMode:  c.Config.Database.Postgres.SSLMode,
		LogLevel: logLevel,
	}

	db, err := postgres.NewDB(dbCfg)
	if err != nil {
		return err
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(c.Config.Database.Postgres.Pool.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.Config.Database.Postgres.Pool.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(c.Config.Database.Postgres.Pool.MaxLifetime)
	sqlDB.SetConnMaxIdleTime(c.Config.Database.Postgres.Pool.MaxIdleTime)

	c.DB = db

	// Auto-migrate domain models
	if err := postgres.AutoMigrate(db); err != nil {
		return fmt.Errorf("auto-migration failed: %w", err)
	}

	logger.Info("Database connected and migrated")
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
	)

	c.IdleDetector = usecase.NewIdleDetector(
		c.WorkloadRepo,
		c.MetricsRepo,
		c.CostRepo,
		c.EventPublisher,
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
	)

	// Wire prediction engine into decision engine for predictive scaling
	c.DecisionEngine.SetPredictionEngine(c.PredictionEngine)

	c.CapacitySimulator = usecase.NewCapacitySimulator(
		c.WorkloadRepo,
		c.PredictionEngine,
	)

	c.MetricsCleaner = usecase.NewMetricsCleaner(c.MetricsRepo, 7)

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
	)

	if !c.Config.Server.GRPC.Enabled {
		logger.Info("gRPC server disabled (set server.grpc.enabled=true to enable)")
		return
	}

	c.GRPCServer = grpcHandler.NewServer(grpcHandler.ServerConfig{
		Host: c.Config.Server.GRPC.Host,
		Port: c.Config.Server.GRPC.Port,
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
	return c.DeployObserver.OnDeployCompleted(ctx, envID)
}

// handleAlertFired handles monitor node alerts from ops-service
func (c *Container) handleAlertFired(ctx context.Context, event events.CloudEvent) error {
	logger.Info("Monitor alert fired — evaluating scaling response")
	return nil
}

// Close cleans up all resources
func (c *Container) Close() {
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
