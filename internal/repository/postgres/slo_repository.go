package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// SLORepository handles SLO definition and burn rate log persistence
type SLORepository struct {
	db *gorm.DB
}

func NewSLORepository(db *gorm.DB) *SLORepository {
	return &SLORepository{db: db}
}

func (r *SLORepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db
}

func (r *SLORepository) CreateDefinition(ctx context.Context, d *domain.SLODefinition) error {
	return r.getDB(ctx).Create(d).Error
}

func (r *SLORepository) FindDefinitionsByWorkload(ctx context.Context, workloadID uuid.UUID) ([]*domain.SLODefinition, error) {
	var defs []*domain.SLODefinition
	result := r.getDB(ctx).
		Where("workload_id = ? AND enabled = ?", workloadID, true).
		Find(&defs)
	return defs, result.Error
}

func (r *SLORepository) FindDefinitionByID(ctx context.Context, id uuid.UUID) (*domain.SLODefinition, error) {
	var d domain.SLODefinition
	result := r.getDB(ctx).Where("id = ?", id).First(&d)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrSLONotFound
		}
		return nil, result.Error
	}
	return &d, nil
}

func (r *SLORepository) UpdateDefinition(ctx context.Context, d *domain.SLODefinition) error {
	return r.getDB(ctx).Save(d).Error
}

func (r *SLORepository) CreateBurnRateLog(ctx context.Context, log *domain.SLOBurnRateLog) error {
	return r.getDB(ctx).Create(log).Error
}
