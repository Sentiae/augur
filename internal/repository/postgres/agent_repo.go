package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

// AgentRepository handles agent identity + enrollment token persistence.
type AgentRepository struct {
	db *gorm.DB
}

func NewAgentRepository(db *gorm.DB) *AgentRepository {
	return &AgentRepository{db: db}
}

func (r *AgentRepository) getDB(ctx context.Context) *gorm.DB {
	if tx := TxFromContext(ctx); tx != nil {
		return tx
	}
	return r.db.WithContext(ctx)
}

// SaveAgent upserts an agent by primary key.
func (r *AgentRepository) SaveAgent(ctx context.Context, a *domain.Agent) error {
	return r.getDB(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(a).Error
}

func (r *AgentRepository) FindAgentByID(ctx context.Context, id uuid.UUID) (*domain.Agent, error) {
	var a domain.Agent
	result := r.getDB(ctx).Where("id = ?", id).First(&a)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrAgentNotFound
		}
		return nil, result.Error
	}
	return &a, nil
}

// SaveEnrollmentToken upserts an enrollment token by primary key.
func (r *AgentRepository) SaveEnrollmentToken(ctx context.Context, t *domain.EnrollmentToken) error {
	return r.getDB(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(t).Error
}

func (r *AgentRepository) FindEnrollmentTokenByHash(ctx context.Context, hash string) (*domain.EnrollmentToken, error) {
	var t domain.EnrollmentToken
	result := r.getDB(ctx).Where("token_hash = ?", hash).First(&t)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, domain.ErrEnrollmentTokenNotFound
		}
		return nil, result.Error
	}
	return &t, nil
}

// ConsumeEnrollmentToken atomically marks an unconsumed token consumed. If the
// row was already consumed (or does not exist), it returns
// ErrEnrollmentTokenConsumed — the single-use guarantee is enforced in SQL, not
// by a read-then-write race.
func (r *AgentRepository) ConsumeEnrollmentToken(ctx context.Context, tokenID uuid.UUID, now time.Time) error {
	result := r.getDB(ctx).Model(&domain.EnrollmentToken{}).
		Where("id = ? AND consumed_at IS NULL", tokenID).
		Update("consumed_at", now.UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrEnrollmentTokenConsumed
	}
	return nil
}
