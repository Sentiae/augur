package usecase

import (
	"context"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
)

// AlertService manages alert queries
type AlertService struct {
	alertRepo *postgres.AlertRepository
}

func NewAlertService(alertRepo *postgres.AlertRepository) *AlertService {
	return &AlertService{alertRepo: alertRepo}
}

// ListAlerts returns active alerts for an organization with optional filters
func (s *AlertService) ListAlerts(ctx context.Context, orgID uuid.UUID, severity, alertType string) ([]*domain.AugurAlert, error) {
	return s.alertRepo.FindActiveFiltered(ctx, orgID, severity, alertType)
}

// CountActiveAlerts returns the count of active alerts for an organization
func (s *AlertService) CountActiveAlerts(ctx context.Context, orgID uuid.UUID) (int64, error) {
	return s.alertRepo.CountActiveByOrg(ctx, orgID)
}
