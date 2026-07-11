package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// WorkloadRepository handles workload persistence
type WorkloadRepository struct {
	db *gorm.DB
}

func NewWorkloadRepository(db *gorm.DB) *WorkloadRepository {
	return &WorkloadRepository{db: db}
}

func (r *WorkloadRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

func (r *WorkloadRepository) Create(ctx context.Context, w *domain.Workload) error {
	return r.getDB(ctx).Create(w).Error
}

func (r *WorkloadRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Workload, error) {
	var w domain.Workload
	result := r.getDB(ctx).Where("id = ?", id).First(&w)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrWorkloadNotFound
		}
		return nil, result.Error
	}
	return &w, nil
}

func (r *WorkloadRepository) FindByOrganization(ctx context.Context, orgID uuid.UUID) ([]*domain.Workload, error) {
	var workloads []*domain.Workload
	result := r.getDB(ctx).Where("organization_id = ?", orgID).Order("name").Find(&workloads)
	return workloads, result.Error
}

func (r *WorkloadRepository) FindByOrganizationFiltered(ctx context.Context, orgID uuid.UUID, env, group string, featureID *uuid.UUID) ([]*domain.Workload, error) {
	q := r.getDB(ctx).Where("organization_id = ?", orgID)
	if env != "" {
		q = q.Where("environment = ?", env)
	}
	if group != "" {
		q = q.Where("group_name = ?", group)
	}
	if featureID != nil {
		q = q.Where("feature_id = ?", *featureID)
	}
	var workloads []*domain.Workload
	result := q.Order("name").Find(&workloads)
	return workloads, result.Error
}

func (r *WorkloadRepository) FindActive(ctx context.Context) ([]*domain.Workload, error) {
	var workloads []*domain.Workload
	result := r.getDB(ctx).
		Where("autoscaling_enabled = ? AND autoscaling_paused = ? AND observe_mode = ?", true, false, false).
		Find(&workloads)
	return workloads, result.Error
}

func (r *WorkloadRepository) FindAllManaged(ctx context.Context) ([]*domain.Workload, error) {
	var workloads []*domain.Workload
	result := r.getDB(ctx).Find(&workloads)
	return workloads, result.Error
}

func (r *WorkloadRepository) Update(ctx context.Context, w *domain.Workload) error {
	return r.getDB(ctx).Save(w).Error
}

func (r *WorkloadRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.getDB(ctx).Where("id = ?", id).Delete(&domain.Workload{}).Error
}

func (r *WorkloadRepository) CountByOrganization(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	result := r.getDB(ctx).Model(&domain.Workload{}).Where("organization_id = ?", orgID).Count(&count)
	return count, result.Error
}
