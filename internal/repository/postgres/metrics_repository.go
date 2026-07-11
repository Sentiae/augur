package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// MetricsRepository handles workload metrics snapshot persistence
type MetricsRepository struct {
	db *gorm.DB
}

func NewMetricsRepository(db *gorm.DB) *MetricsRepository {
	return &MetricsRepository{db: db}
}

func (r *MetricsRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

func (r *MetricsRepository) Create(ctx context.Context, m *domain.WorkloadMetricsSnapshot) error {
	return r.getDB(ctx).Create(m).Error
}

func (r *MetricsRepository) FindByWorkload(ctx context.Context, workloadID uuid.UUID, since time.Time, limit int) ([]*domain.WorkloadMetricsSnapshot, error) {
	var metrics []*domain.WorkloadMetricsSnapshot
	result := r.getDB(ctx).
		Where("workload_id = ? AND timestamp >= ?", workloadID, since).
		Order("timestamp DESC").
		Limit(limit).
		Find(&metrics)
	return metrics, result.Error
}

func (r *MetricsRepository) FindLatest(ctx context.Context, workloadID uuid.UUID) (*domain.WorkloadMetricsSnapshot, error) {
	var m domain.WorkloadMetricsSnapshot
	result := r.getDB(ctx).
		Where("workload_id = ?", workloadID).
		Order("timestamp DESC").
		First(&m)
	if result.Error != nil {
		return nil, result.Error
	}
	return &m, nil
}

func (r *MetricsRepository) DeleteOlderThan(ctx context.Context, before time.Time) error {
	return r.getDB(ctx).
		Where("timestamp < ?", before).
		Delete(&domain.WorkloadMetricsSnapshot{}).Error
}
