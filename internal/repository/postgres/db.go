package postgres

import (
	"context"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// Config holds database connection parameters
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	LogLevel gormlogger.LogLevel
}

// NewDB creates a new GORM database connection
func NewDB(cfg Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(cfg.LogLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return db, nil
}

// AutoMigrate runs GORM auto-migration for all domain models
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&domain.Workload{},
		&domain.WorkloadMetricsSnapshot{},
		&domain.ScalingDecision{},
		&domain.AugurPolicy{},
		&domain.SLODefinition{},
		&domain.SLOBurnRateLog{},
		&domain.AugurAlert{},
		&domain.CostBudget{},
		&domain.IdleResource{},
		&domain.ModelRegistry{},
	)
}

// contextKey for transaction handling
type contextKey string

const txKey contextKey = "augur_tx"

// TxFromContext returns a transaction from context if available
func TxFromContext(ctx context.Context) *gorm.DB {
	tx, ok := ctx.Value(txKey).(*gorm.DB)
	if !ok {
		return nil
	}
	return tx
}

// ContextWithTx stores a transaction in context
func ContextWithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey, tx)
}
