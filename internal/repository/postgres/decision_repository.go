package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// DecisionRepository handles scaling decision persistence
type DecisionRepository struct {
	db *gorm.DB
}

func NewDecisionRepository(db *gorm.DB) *DecisionRepository {
	return &DecisionRepository{db: db}
}

func (r *DecisionRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db
}

func (r *DecisionRepository) Create(ctx context.Context, d *domain.ScalingDecision) error {
	return r.getDB(ctx).Create(d).Error
}

func (r *DecisionRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.ScalingDecision, error) {
	var d domain.ScalingDecision
	result := r.getDB(ctx).Where("id = ?", id).First(&d)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrDecisionNotFound
		}
		return nil, result.Error
	}
	return &d, nil
}

func (r *DecisionRepository) FindByWorkload(ctx context.Context, workloadID uuid.UUID, limit int) ([]*domain.ScalingDecision, error) {
	var decisions []*domain.ScalingDecision
	result := r.getDB(ctx).
		Where("workload_id = ?", workloadID).
		Order("decided_at DESC").
		Limit(limit).
		Find(&decisions)
	return decisions, result.Error
}

func (r *DecisionRepository) CountRecentByWorkload(ctx context.Context, workloadID uuid.UUID, since time.Time) (int64, error) {
	var count int64
	result := r.getDB(ctx).Model(&domain.ScalingDecision{}).
		Where("workload_id = ? AND decided_at > ? AND dry_run = ?", workloadID, since, false).
		Count(&count)
	return count, result.Error
}

func (r *DecisionRepository) FindLastByWorkload(ctx context.Context, workloadID uuid.UUID) (*domain.ScalingDecision, error) {
	var d domain.ScalingDecision
	result := r.getDB(ctx).
		Where("workload_id = ? AND dry_run = ?", workloadID, false).
		Order("decided_at DESC").
		First(&d)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &d, nil
}

func (r *DecisionRepository) Update(ctx context.Context, d *domain.ScalingDecision) error {
	return r.getDB(ctx).Save(d).Error
}

func (r *DecisionRepository) FindPendingOutcomes(ctx context.Context, olderThan time.Time) ([]*domain.ScalingDecision, error) {
	var decisions []*domain.ScalingDecision
	result := r.getDB(ctx).
		Where("outcome = ? AND decided_at < ?", domain.DecisionOutcomePending, olderThan).
		Find(&decisions)
	return decisions, result.Error
}
