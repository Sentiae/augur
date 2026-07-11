package usecase

import (
	"context"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// MetricsCleaner periodically removes old metrics snapshots to prevent unbounded growth.
// Retention: 7 days at full resolution (matching VictoriaMetrics raw retention).
type MetricsCleaner struct {
	metricsRepo  *postgres.MetricsRepository
	orgLister    OrgLister
	retentionDays int
}

func NewMetricsCleaner(metricsRepo *postgres.MetricsRepository, orgLister OrgLister, retentionDays int) *MetricsCleaner {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	return &MetricsCleaner{
		metricsRepo:   metricsRepo,
		orgLister:     orgLister,
		retentionDays: retentionDays,
	}
}

// Run starts the cleanup loop (every 6 hours)
func (c *MetricsCleaner) Run(ctx context.Context) {
	logger.Info("Metrics cleaner started (retention=%d days, interval=6h)", c.retentionDays)
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Metrics cleaner stopped")
			return
		case <-ticker.C:
			c.cleanup(ctx)
		}
	}
}

func (c *MetricsCleaner) cleanup(ctx context.Context) {
	_ = ForEachOrg(ctx, c.orgLister, func(orgCtx context.Context) error {
		cutoff := time.Now().Add(-time.Duration(c.retentionDays) * 24 * time.Hour)
		if err := c.metricsRepo.DeleteOlderThan(orgCtx, cutoff); err != nil {
			logger.Error("Metrics cleaner: failed to delete old metrics: %v", err)
			return err
		}
		logger.Info("Metrics cleaner: deleted metrics older than %s", cutoff.Format(time.RFC3339))
		return nil
	})
}
