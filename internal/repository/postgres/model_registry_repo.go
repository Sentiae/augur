package postgres

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// ModelRegistryRepo handles ONNX model version persistence
type ModelRegistryRepo struct {
	db *gorm.DB
}

func NewModelRegistryRepo(db *gorm.DB) *ModelRegistryRepo {
	return &ModelRegistryRepo{db: db}
}

func (r *ModelRegistryRepo) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db
}

func (r *ModelRegistryRepo) Create(ctx context.Context, m *domain.ModelRegistry) error {
	return r.getDB(ctx).Create(m).Error
}

func (r *ModelRegistryRepo) FindActive(ctx context.Context, modelType string) (*domain.ModelRegistry, error) {
	var m domain.ModelRegistry
	result := r.getDB(ctx).
		Where("model_type = ? AND active = ?", modelType, true).
		Order("created_at DESC").
		First(&m)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, result.Error
	}
	return &m, nil
}

func (r *ModelRegistryRepo) ListByType(ctx context.Context, modelType string) ([]*domain.ModelRegistry, error) {
	var models []*domain.ModelRegistry
	result := r.getDB(ctx).
		Where("model_type = ?", modelType).
		Order("created_at DESC").
		Find(&models)
	return models, result.Error
}

func (r *ModelRegistryRepo) SetActive(ctx context.Context, modelType string, modelID string) error {
	// Deactivate all models of this type
	if err := r.getDB(ctx).Model(&domain.ModelRegistry{}).
		Where("model_type = ?", modelType).
		Update("active", false).Error; err != nil {
		return err
	}
	// Activate the specified model
	return r.getDB(ctx).Model(&domain.ModelRegistry{}).
		Where("id = ?", modelID).
		Update("active", true).Error
}
