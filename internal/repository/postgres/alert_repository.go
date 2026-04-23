package postgres

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// AlertRepository handles alert persistence
type AlertRepository struct {
	db *gorm.DB
}

func NewAlertRepository(db *gorm.DB) *AlertRepository {
	return &AlertRepository{db: db}
}

func (r *AlertRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db
}

func (r *AlertRepository) Create(ctx context.Context, a *domain.AugurAlert) error {
	return r.getDB(ctx).Create(a).Error
}

func (r *AlertRepository) FindActive(ctx context.Context, orgID uuid.UUID) ([]*domain.AugurAlert, error) {
	var alerts []*domain.AugurAlert
	result := r.getDB(ctx).
		Where("organization_id = ? AND resolved_at IS NULL", orgID).
		Order("fired_at DESC").
		Find(&alerts)
	return alerts, result.Error
}

func (r *AlertRepository) FindActiveFiltered(ctx context.Context, orgID uuid.UUID, severity string, alertType string) ([]*domain.AugurAlert, error) {
	q := r.getDB(ctx).Where("organization_id = ? AND resolved_at IS NULL", orgID)
	if severity != "" {
		q = q.Where("severity = ?", severity)
	}
	if alertType != "" {
		q = q.Where("type = ?", alertType)
	}
	var alerts []*domain.AugurAlert
	result := q.Order("fired_at DESC").Find(&alerts)
	return alerts, result.Error
}

func (r *AlertRepository) FindByWorkload(ctx context.Context, workloadID uuid.UUID) ([]*domain.AugurAlert, error) {
	var alerts []*domain.AugurAlert
	result := r.getDB(ctx).
		Where("workload_id = ? AND resolved_at IS NULL", workloadID).
		Order("fired_at DESC").
		Find(&alerts)
	return alerts, result.Error
}

func (r *AlertRepository) Update(ctx context.Context, a *domain.AugurAlert) error {
	return r.getDB(ctx).Save(a).Error
}

func (r *AlertRepository) CountActiveByOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	result := r.getDB(ctx).Model(&domain.AugurAlert{}).
		Where("organization_id = ? AND resolved_at IS NULL", orgID).
		Count(&count)
	return count, result.Error
}
