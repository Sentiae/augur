package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// PolicyRepository handles policy persistence
type PolicyRepository struct {
	db *gorm.DB
}

func NewPolicyRepository(db *gorm.DB) *PolicyRepository {
	return &PolicyRepository{db: db}
}

func (r *PolicyRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

func (r *PolicyRepository) Create(ctx context.Context, p *domain.AugurPolicy) error {
	return r.getDB(ctx).Create(p).Error
}

func (r *PolicyRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.AugurPolicy, error) {
	var p domain.AugurPolicy
	result := r.getDB(ctx).Where("id = ?", id).First(&p)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrPolicyNotFound
		}
		return nil, result.Error
	}
	return &p, nil
}

func (r *PolicyRepository) FindGlobal(ctx context.Context, orgID uuid.UUID) (*domain.AugurPolicy, error) {
	var p domain.AugurPolicy
	result := r.getDB(ctx).
		Where("organization_id = ? AND scope = ? AND enabled = ?", orgID, domain.PolicyScopeGlobal, true).
		Order("priority DESC").
		First(&p)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &p, nil
}

func (r *PolicyRepository) FindByGroup(ctx context.Context, orgID uuid.UUID, groupName string) (*domain.AugurPolicy, error) {
	var p domain.AugurPolicy
	result := r.getDB(ctx).
		Where("organization_id = ? AND scope = ? AND scope_id = ? AND enabled = ?", orgID, domain.PolicyScopeGroup, groupName, true).
		Order("priority DESC").
		First(&p)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &p, nil
}

func (r *PolicyRepository) FindByApp(ctx context.Context, orgID uuid.UUID, workloadID string) (*domain.AugurPolicy, error) {
	var p domain.AugurPolicy
	result := r.getDB(ctx).
		Where("organization_id = ? AND scope = ? AND scope_id = ? AND enabled = ?", orgID, domain.PolicyScopeApp, workloadID, true).
		Order("priority DESC").
		First(&p)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &p, nil
}

func (r *PolicyRepository) FindByOrganization(ctx context.Context, orgID uuid.UUID) ([]*domain.AugurPolicy, error) {
	var policies []*domain.AugurPolicy
	result := r.getDB(ctx).
		Where("organization_id = ? AND enabled = ?", orgID, true).
		Order("scope, priority DESC").
		Find(&policies)
	return policies, result.Error
}

func (r *PolicyRepository) Update(ctx context.Context, p *domain.AugurPolicy) error {
	return r.getDB(ctx).Save(p).Error
}

func (r *PolicyRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.getDB(ctx).Where("id = ?", id).Delete(&domain.AugurPolicy{}).Error
}
