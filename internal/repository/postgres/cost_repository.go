package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// CostRepository handles cost budget and idle resource persistence
type CostRepository struct {
	db *gorm.DB
}

func NewCostRepository(db *gorm.DB) *CostRepository {
	return &CostRepository{db: db}
}

func (r *CostRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

// Budget operations

func (r *CostRepository) CreateBudget(ctx context.Context, b *domain.CostBudget) error {
	return r.getDB(ctx).Create(b).Error
}

func (r *CostRepository) FindBudget(ctx context.Context, scope, scopeID string) (*domain.CostBudget, error) {
	var b domain.CostBudget
	result := r.getDB(ctx).
		Where("scope = ? AND scope_id = ? AND enabled = ?", scope, scopeID, true).
		First(&b)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &b, nil
}

func (r *CostRepository) FindBudgetsByOrg(ctx context.Context, orgID uuid.UUID) ([]*domain.CostBudget, error) {
	var budgets []*domain.CostBudget
	result := r.getDB(ctx).
		Where("organization_id = ? AND enabled = ?", orgID, true).
		Find(&budgets)
	return budgets, result.Error
}

func (r *CostRepository) UpdateBudget(ctx context.Context, b *domain.CostBudget) error {
	return r.getDB(ctx).Save(b).Error
}

// Idle resource operations

func (r *CostRepository) CreateIdleResource(ctx context.Context, res *domain.IdleResource) error {
	return r.getDB(ctx).Create(res).Error
}

func (r *CostRepository) FindIdleResources(ctx context.Context, orgID uuid.UUID, resourceType string, minIdleDays int) ([]*domain.IdleResource, error) {
	q := r.getDB(ctx).Where("organization_id = ? AND decommissioned = ?", orgID, false)
	if resourceType != "" {
		q = q.Where("resource_type = ?", resourceType)
	}
	if minIdleDays > 0 {
		q = q.Where("idle_since_days >= ?", minIdleDays)
	}
	var resources []*domain.IdleResource
	result := q.Order("estimated_monthly_waste_usd DESC").Find(&resources)
	return resources, result.Error
}

func (r *CostRepository) UpdateIdleResource(ctx context.Context, res *domain.IdleResource) error {
	return r.getDB(ctx).Save(res).Error
}
