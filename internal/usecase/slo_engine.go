package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
)

// SLOEngine computes SLO compliance and burn rates
type SLOEngine struct {
	sloRepo      *postgres.SLORepository
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
	alertRepo    *postgres.AlertRepository
	publisher    events.EventPublisher
}

func NewSLOEngine(
	sloRepo *postgres.SLORepository,
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
	alertRepo *postgres.AlertRepository,
	publisher events.EventPublisher,
) *SLOEngine {
	return &SLOEngine{
		sloRepo:      sloRepo,
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
		alertRepo:    alertRepo,
		publisher:    publisher,
	}
}

// GetSLOStatus computes the current SLO status for a workload
func (e *SLOEngine) GetSLOStatus(ctx context.Context, workloadID uuid.UUID) (*domain.SLOStatus, error) {
	w, err := e.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	defs, err := e.sloRepo.FindDefinitionsByWorkload(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if len(defs) == 0 {
		// No SLOs defined — return healthy default
		return &domain.SLOStatus{
			WorkloadID:              workloadID,
			SLOTargetPct:            99.9,
			CurrentCompliancePct:    100,
			ErrorBudgetRemainingPct: 100,
			BurnRates: domain.BurnRates{
				Window1h: 0,
				Window6h: 0,
				Window1d: 0,
				Window3d: 0,
			},
			Mode:              domain.SLOModeNormal,
			FreezeRecommended: false,
		}, nil
	}

	// Use the most restrictive SLO definition
	primarySLO := defs[0]

	// Compute burn rates from metrics
	burnRates := e.computeBurnRates(ctx, workloadID, primarySLO)

	// Compute error budget
	budgetRemaining := e.computeErrorBudget(w, primarySLO)

	mode := domain.ComputeSLOMode(budgetRemaining, burnRates)

	status := &domain.SLOStatus{
		WorkloadID:              workloadID,
		SLOTargetPct:            primarySLO.TargetPct,
		CurrentCompliancePct:    w.SLOCompliancePct,
		ErrorBudgetRemainingPct: budgetRemaining,
		BurnRates:               burnRates,
		Mode:                    mode,
		FreezeRecommended:       mode == domain.SLOModeEmergency,
	}

	// Publish breach events if needed
	if mode == domain.SLOModeCritical || mode == domain.SLOModeEmergency {
		featureID := ""
		if w.FeatureID != nil {
			featureID = w.FeatureID.String()
		}
		_ = e.publisher.Publish(ctx, events.EventSLOBreachDetected, events.EventData{
			ResourceID:   workloadID.String(),
			ResourceType: "workload",
			Metadata: map[string]any{
				"feature_id":      featureID,
				"slo_type":        string(primarySLO.SLOType),
				"slo_target":      primarySLO.TargetPct,
				"current_value":   w.SLOCompliancePct,
				"burn_rate":       burnRates.Window1h,
				"window":          "1h",
				"budget_pct_left": budgetRemaining,
				"action_taken":    fmt.Sprintf("SLO mode escalated to %s", mode),
			},
		})
	}

	if budgetRemaining <= 20 && budgetRemaining > 5 {
		_ = e.publisher.Publish(ctx, events.EventSLOBudgetWarning, events.EventData{
			ResourceID:   workloadID.String(),
			ResourceType: "workload",
			Metadata: map[string]any{
				"budget_pct_left": budgetRemaining,
			},
		})
	}

	return status, nil
}

// CreateSLO creates a new SLO definition for a workload
func (e *SLOEngine) CreateSLO(ctx context.Context, def *domain.SLODefinition) error {
	def.ID = uuid.New()
	return e.sloRepo.CreateDefinition(ctx, def)
}

func (e *SLOEngine) computeBurnRates(ctx context.Context, workloadID uuid.UUID, slo *domain.SLODefinition) domain.BurnRates {
	// Compute burn rates from error rate metrics across windows
	// Burn rate = (error rate / allowed error rate)
	allowedErrorRate := 100 - slo.TargetPct // e.g., 0.1% for 99.9% SLO

	if allowedErrorRate <= 0 {
		return domain.BurnRates{}
	}

	windows := map[string]time.Duration{
		"1h": 1 * time.Hour,
		"6h": 6 * time.Hour,
		"1d": 24 * time.Hour,
		"3d": 72 * time.Hour,
	}

	rates := domain.BurnRates{}

	for window, dur := range windows {
		metrics, err := e.metricsRepo.FindByWorkload(ctx, workloadID, time.Now().Add(-dur), 1000)
		if err != nil || len(metrics) == 0 {
			continue
		}

		// Average error rate over the window
		var totalErr float64
		for _, m := range metrics {
			totalErr += m.ErrorRatePct
		}
		avgErr := totalErr / float64(len(metrics))

		burnRate := avgErr / allowedErrorRate

		switch window {
		case "1h":
			rates.Window1h = burnRate
		case "6h":
			rates.Window6h = burnRate
		case "1d":
			rates.Window1d = burnRate
		case "3d":
			rates.Window3d = burnRate
		}
	}

	return rates
}

func (e *SLOEngine) computeErrorBudget(w *domain.Workload, slo *domain.SLODefinition) float64 {
	allowedErrorRate := 100 - slo.TargetPct
	if allowedErrorRate <= 0 {
		return 0
	}

	// Budget consumed = current error rate / allowed error rate * 100
	consumed := (w.ErrorRatePct / allowedErrorRate) * 100
	remaining := 100 - consumed
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	return remaining
}

// LogBurnRate persists a burn rate snapshot
func (e *SLOEngine) LogBurnRate(ctx context.Context, workloadID uuid.UUID, sloDefID uuid.UUID, burnRates domain.BurnRates, budgetPct float64, mode domain.SLOMode) error {
	def, err := e.sloRepo.FindDefinitionByID(ctx, sloDefID)
	if err != nil {
		return err
	}
	log := &domain.SLOBurnRateLog{
		ID:                      uuid.New(),
		WorkloadID:              workloadID,
		SLODefinitionID:         sloDefID,
		OrganizationID:          def.OrganizationID,
		BurnRate1h:              burnRates.Window1h,
		BurnRate6h:              burnRates.Window6h,
		BurnRate1d:              burnRates.Window1d,
		BurnRate3d:              burnRates.Window3d,
		ErrorBudgetRemainingPct: budgetPct,
		Mode:                    mode,
		Timestamp:               time.Now(),
	}
	return e.sloRepo.CreateBurnRateLog(ctx, log)
}

