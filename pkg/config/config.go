package config

import (
	"fmt"
	"strings"
	"time"

	pkconfig "github.com/sentiae/platform-kit/config"
)

// Config represents the complete application configuration
type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Cache     CacheConfig     `mapstructure:"cache"`
	Messaging MessagingConfig `mapstructure:"messaging"`
	Services  ServicesConfig  `mapstructure:"services"`
	Security  SecurityConfig  `mapstructure:"security"`
	Features      FeaturesConfig      `mapstructure:"features"`
	Observability ObservabilityConfig `mapstructure:"observability"`
	Engine        EngineConfig        `mapstructure:"engine"`
}

// AppConfig contains application metadata
type AppConfig struct {
	Name        string `mapstructure:"name"`
	Version     string `mapstructure:"version"`
	Environment string `mapstructure:"environment"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

// ServerConfig contains server configuration
type ServerConfig struct {
	HTTP HTTPConfig `mapstructure:"http"`
	GRPC GRPCConfig `mapstructure:"grpc"`
}

type HTTPConfig struct {
	Enabled  bool           `mapstructure:"enabled"`
	Host     string         `mapstructure:"host"`
	Port     string         `mapstructure:"port"`
	BasePath string         `mapstructure:"base_path"`
	Timeouts TimeoutsConfig `mapstructure:"timeouts"`
}

type TimeoutsConfig struct {
	Read     time.Duration `mapstructure:"read"`
	Write    time.Duration `mapstructure:"write"`
	Idle     time.Duration `mapstructure:"idle"`
	Shutdown time.Duration `mapstructure:"shutdown"`
}

type GRPCConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Host    string `mapstructure:"host"`
	Port    string `mapstructure:"port"`
}

// DatabaseConfig contains database configuration
type DatabaseConfig struct {
	Postgres PostgresConfig `mapstructure:"postgres"`
}

type PostgresConfig struct {
	Host     string     `mapstructure:"host"`
	Port     string     `mapstructure:"port"`
	User     string     `mapstructure:"user"`
	Password string     `mapstructure:"password"`
	Database string     `mapstructure:"database"`
	SSLMode  string     `mapstructure:"ssl_mode"`
	Pool     PoolConfig `mapstructure:"pool"`
	LogLevel string     `mapstructure:"log_level"`
}

type PoolConfig struct {
	MaxOpenConns int           `mapstructure:"max_open_conns"`
	MaxIdleConns int           `mapstructure:"max_idle_conns"`
	MaxLifetime  time.Duration `mapstructure:"max_lifetime"`
	MaxIdleTime  time.Duration `mapstructure:"max_idle_time"`
}

// CacheConfig contains cache configuration
type CacheConfig struct {
	Redis RedisConfig `mapstructure:"redis"`
}

type RedisConfig struct {
	Enabled    bool          `mapstructure:"enabled"`
	Host       string        `mapstructure:"host"`
	Port       string        `mapstructure:"port"`
	Password   string        `mapstructure:"password"`
	Database   int           `mapstructure:"database"`
	KeyPrefix  string        `mapstructure:"key_prefix"`
	DefaultTTL time.Duration `mapstructure:"default_ttl"`
}

// MessagingConfig contains messaging configuration
type MessagingConfig struct {
	Kafka KafkaConfig `mapstructure:"kafka"`
}

type KafkaConfig struct {
	Enabled  bool              `mapstructure:"enabled"`
	Brokers  string            `mapstructure:"brokers"`
	ClientID string            `mapstructure:"client_id"`
	GroupID  string            `mapstructure:"group_id"`
	Topics   KafkaTopicsConfig `mapstructure:"topics"`
}

type KafkaTopicsConfig struct {
	Prefix      string `mapstructure:"prefix"`
	AugurEvents string `mapstructure:"augur_events"`
	OpsEvents   string `mapstructure:"ops_events"`
}

// ServicesConfig contains external service endpoints
type ServicesConfig struct {
	Identity ServiceEndpoint `mapstructure:"identity"`
	Ops      ServiceEndpoint `mapstructure:"ops"`
	Foundry  ServiceEndpoint `mapstructure:"foundry"`
	Work     ServiceEndpoint `mapstructure:"work"`
}

type ServiceEndpoint struct {
	Enabled      bool          `mapstructure:"enabled"`
	URL          string        `mapstructure:"url"`
	Timeout      time.Duration `mapstructure:"timeout"`
	Retries      int           `mapstructure:"retries"`
	TLSEnabled   bool          `mapstructure:"tls_enabled"`
	ServiceToken string        `mapstructure:"service_token"`
}

// SecurityConfig contains security settings
type SecurityConfig struct {
	JWT JWTConfig `mapstructure:"jwt"`
}

type JWTConfig struct {
	Secret    string        `mapstructure:"secret"`
	Issuer    string        `mapstructure:"issuer"`
	Audience  string        `mapstructure:"audience"`
	Expiry    time.Duration `mapstructure:"expiry"`
	Algorithm string        `mapstructure:"algorithm"`
}

// FeaturesConfig contains feature flags
type FeaturesConfig struct {
	EventPublishing   bool `mapstructure:"event_publishing"`
	MetricsCollection bool `mapstructure:"metrics_collection"`
	MLPrediction      bool `mapstructure:"ml_prediction"`
	SpecAutoCreation  bool `mapstructure:"spec_auto_creation"`
}

// ObservabilityConfig contains VictoriaMetrics / time-series push settings
type ObservabilityConfig struct {
	VictoriaMetrics VMConfig `mapstructure:"victoriametrics"`
}

type VMConfig struct {
	Enabled   bool          `mapstructure:"enabled"`
	URL       string        `mapstructure:"url"`
	AuthToken string        `mapstructure:"auth_token"`
	Timeout   time.Duration `mapstructure:"timeout"`
	FlushSize int           `mapstructure:"flush_size"`
}

// EngineConfig contains decision engine settings
type EngineConfig struct {
	DecisionIntervalSec    int           `mapstructure:"decision_interval_sec"`
	ObservationPeriodDays  int           `mapstructure:"observation_period_days"`
	CooldownScaleUp       time.Duration `mapstructure:"cooldown_scale_up"`
	CooldownScaleDown     time.Duration `mapstructure:"cooldown_scale_down"`
	MaxActionsPerHour      int           `mapstructure:"max_actions_per_hour"`
	CircuitBreakerThreshold int          `mapstructure:"circuit_breaker_threshold"`
	RollbackWindowMin      int           `mapstructure:"rollback_window_min"`
	PostDeployObserveMin   int           `mapstructure:"post_deploy_observe_min"`
}

// Load loads configuration using platform-kit Viper-based loader
func Load() (*Config, error) {
	var cfg Config

	err := pkconfig.Load(&cfg, pkconfig.Options{
		EnvPrefix:   "APP",
		ConfigPaths: []string{"configs", "."},
		Defaults: map[string]any{
			// App
			"app.name":        "infrastructure-intelligence-service",
			"app.version":     "dev",
			"app.environment": "development",

			// Logging
			"logging.level":  "info",
			"logging.format": "json",
			"logging.output": "stdout",

			// Server - HTTP
			"server.http.enabled":           true,
			"server.http.host":              "0.0.0.0",
			"server.http.port":              "8089",
			"server.http.base_path":         "/api/v1",
			"server.http.timeouts.read":     "15s",
			"server.http.timeouts.write":    "15s",
			"server.http.timeouts.idle":     "60s",
			"server.http.timeouts.shutdown": "30s",

			// Server - gRPC
			"server.grpc.enabled": true,
			"server.grpc.host":    "0.0.0.0",
			"server.grpc.port":    "50059",

			// Database
			"database.postgres.host":                "localhost",
			"database.postgres.port":                "5432",
			"database.postgres.user":                "postgres",
			"database.postgres.password":            "postgres",
			"database.postgres.database":            "augur_service",
			"database.postgres.ssl_mode":            "disable",
			"database.postgres.pool.max_open_conns": 25,
			"database.postgres.pool.max_idle_conns": 10,
			"database.postgres.pool.max_lifetime":   "5m",
			"database.postgres.pool.max_idle_time":  "10m",
			"database.postgres.log_level":           "warn",

			// Cache
			"cache.redis.enabled":     true,
			"cache.redis.host":        "localhost",
			"cache.redis.port":        "6379",
			"cache.redis.password":    "",
			"cache.redis.database":    8,
			"cache.redis.key_prefix":  "augur",
			"cache.redis.default_ttl": "1h",

			// Messaging - Kafka
			"messaging.kafka.enabled":             true,
			"messaging.kafka.brokers":             "localhost:9092",
			"messaging.kafka.client_id":           "augur-service-1",
			"messaging.kafka.group_id":            "augur-service",
			"messaging.kafka.topics.prefix":       "sentiae",
			"messaging.kafka.topics.augur_events": "augur.events",
			"messaging.kafka.topics.ops_events":   "ops.events",

			// Services
			"services.identity.enabled":     true,
			"services.identity.url":         "identity-service:50051",
			"services.identity.timeout":     "5s",
			"services.identity.retries":     3,
			"services.identity.tls_enabled": false,

			"services.ops.enabled":     true,
			"services.ops.url":         "http://ops-service:8083",
			"services.ops.timeout":     "10s",
			"services.ops.retries":     3,
			"services.ops.tls_enabled": false,

			"services.foundry.enabled":     false,
			"services.foundry.url":         "http://foundry-service:8085",
			"services.foundry.timeout":     "30s",
			"services.foundry.retries":     2,
			"services.foundry.tls_enabled": false,

			"services.work.enabled":     false,
			"services.work.url":         "http://work-service:8080",
			"services.work.timeout":     "10s",
			"services.work.retries":     3,
			"services.work.tls_enabled": false,

			// Security
			"security.jwt.secret":    "change-this-secret-in-production",
			"security.jwt.issuer":    "augur-service",
			"security.jwt.audience":  "sentiae",
			"security.jwt.expiry":    "24h",
			"security.jwt.algorithm": "HS256",

			// Features
			"features.event_publishing":   true,
			"features.metrics_collection": true,
			"features.ml_prediction":      false,
			"features.spec_auto_creation": false,

			// Observability — VictoriaMetrics
			"observability.victoriametrics.enabled":    false,
			"observability.victoriametrics.url":        "http://victoriametrics:8428",
			"observability.victoriametrics.auth_token": "",
			"observability.victoriametrics.timeout":    "5s",
			"observability.victoriametrics.flush_size": 100,

			// Engine
			"engine.decision_interval_sec":     30,
			"engine.observation_period_days":    7,
			"engine.cooldown_scale_up":          "60s",
			"engine.cooldown_scale_down":        "300s",
			"engine.max_actions_per_hour":       6,
			"engine.circuit_breaker_threshold":  3,
			"engine.rollback_window_min":        5,
			"engine.post_deploy_observe_min":    15,
		},
		BindEnvs: [][2]string{
			{"app.name", "APP_APP_NAME"},
			{"app.version", "APP_APP_VERSION"},
			{"app.environment", "APP_APP_ENVIRONMENT"},
			{"logging.level", "APP_LOGGING_LEVEL"},
			{"server.http.port", "APP_SERVER_PORT"},
			{"server.grpc.port", "APP_GRPC_PORT"},
			{"database.postgres.host", "APP_DATABASE_HOST"},
			{"database.postgres.port", "APP_DATABASE_PORT"},
			{"database.postgres.user", "APP_DATABASE_USER"},
			{"database.postgres.password", "APP_DATABASE_PASSWORD"},
			{"database.postgres.database", "APP_DATABASE_NAME"},
			{"database.postgres.ssl_mode", "APP_DATABASE_SSL_MODE"},
			{"cache.redis.host", "APP_REDIS_HOST"},
			{"cache.redis.port", "APP_REDIS_PORT"},
			{"cache.redis.password", "APP_REDIS_PASSWORD"},
			{"cache.redis.database", "APP_REDIS_DB"},
			{"messaging.kafka.enabled", "APP_KAFKA_ENABLED"},
			{"messaging.kafka.brokers", "APP_KAFKA_BROKERS"},
			{"messaging.kafka.client_id", "APP_KAFKA_CLIENT_ID"},
			{"messaging.kafka.group_id", "APP_KAFKA_GROUP_ID"},
			{"services.identity.url", "APP_SERVICES_IDENTITY_URL"},
			{"services.ops.url", "APP_SERVICES_OPS_URL"},
			{"services.foundry.url", "APP_SERVICES_FOUNDRY_URL"},
			{"services.work.url", "APP_SERVICES_WORK_URL"},
			{"security.jwt.secret", "APP_SECURITY_JWT_SECRET"},
			{"features.event_publishing", "APP_FEATURES_EVENT_PUBLISHING"},
			{"features.ml_prediction", "APP_FEATURES_ML_PREDICTION"},
			{"features.spec_auto_creation", "APP_FEATURES_SPEC_AUTO_CREATION"},
			{"observability.victoriametrics.enabled", "APP_VM_ENABLED"},
			{"observability.victoriametrics.url", "APP_VM_URL"},
			{"observability.victoriametrics.auth_token", "APP_VM_AUTH_TOKEN"},
			{"engine.decision_interval_sec", "APP_ENGINE_DECISION_INTERVAL_SEC"},
			{"engine.observation_period_days", "APP_ENGINE_OBSERVATION_PERIOD_DAYS"},
			{"engine.max_actions_per_hour", "APP_ENGINE_MAX_ACTIONS_PER_HOUR"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	return &cfg, nil
}

// GetDatabaseURL returns the PostgreSQL connection URL
func (c *Config) GetDatabaseURL() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Postgres.Host,
		c.Database.Postgres.Port,
		c.Database.Postgres.User,
		c.Database.Postgres.Password,
		c.Database.Postgres.Database,
		c.Database.Postgres.SSLMode,
	)
}

// GetRedisAddr returns the Redis connection address
func (c *Config) GetRedisAddr() string {
	return fmt.Sprintf("%s:%s", c.Cache.Redis.Host, c.Cache.Redis.Port)
}

// GetKafkaBrokers returns Kafka brokers as a slice
func (c *Config) GetKafkaBrokers() []string {
	return strings.Split(c.Messaging.Kafka.Brokers, ",")
}

// GetAugurEventsTopic returns the full Augur events topic name
func (c *Config) GetAugurEventsTopic() string {
	return fmt.Sprintf("%s.%s", c.Messaging.Kafka.Topics.Prefix, c.Messaging.Kafka.Topics.AugurEvents)
}

// GetOpsEventsTopic returns the full ops events topic name
func (c *Config) GetOpsEventsTopic() string {
	return fmt.Sprintf("%s.%s", c.Messaging.Kafka.Topics.Prefix, c.Messaging.Kafka.Topics.OpsEvents)
}
