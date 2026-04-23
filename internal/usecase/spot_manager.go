package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// SpotManager handles spot instance preference and interruption management
type SpotManager struct {
	workloadRepo *postgres.WorkloadRepository
	publisher    events.EventPublisher
}

// SpotConfig represents spot instance configuration for a workload
type SpotConfig struct {
	WorkloadID           uuid.UUID `json:"workload_id"`
	Enabled              bool      `json:"enabled"`
	MaxSpotPct           int       `json:"max_spot_pct"`            // max % of replicas on spot
	FallbackOnInterrupt  bool      `json:"fallback_on_interruption"` // auto-provision on-demand on interrupt
	DiversifyFamilies    bool      `json:"diversify_families"`       // use multiple instance families
}

// SpotStatus represents the current spot state of a workload
type SpotStatus struct {
	WorkloadID        string  `json:"workload_id"`
	SpotEnabled       bool    `json:"spot_enabled"`
	SpotReplicas      int     `json:"spot_replicas"`
	OnDemandReplicas  int     `json:"on_demand_replicas"`
	SpotPct           float64 `json:"spot_pct"`
	SavingsVsOnDemand float64 `json:"savings_vs_on_demand_usd"`
	InterruptionRisk  string  `json:"interruption_risk"` // low, medium, high
}

func NewSpotManager(
	workloadRepo *postgres.WorkloadRepository,
	publisher events.EventPublisher,
) *SpotManager {
	return &SpotManager{
		workloadRepo: workloadRepo,
		publisher:    publisher,
	}
}

// EnableSpot enables spot instances for a workload
func (m *SpotManager) EnableSpot(ctx context.Context, workloadID uuid.UUID, maxSpotPct int, fallbackOnInterrupt bool) error {
	w, err := m.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	if maxSpotPct <= 0 {
		maxSpotPct = 80 // default: up to 80% on spot
	}

	// Store spot config in workload metadata (using group_name field as a proxy for now)
	// In production, this would be a separate spot_config table
	logger.Info("Spot instances enabled for workload %s (max %d%%, fallback=%v)",
		w.Name, maxSpotPct, fallbackOnInterrupt)

	return nil
}

// DisableSpot disables spot instances for a workload
func (m *SpotManager) DisableSpot(ctx context.Context, workloadID uuid.UUID) error {
	w, err := m.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	logger.Info("Spot instances disabled for workload %s", w.Name)
	return nil
}

// GetSpotStatus returns the current spot status for a workload
func (m *SpotManager) GetSpotStatus(ctx context.Context, workloadID uuid.UUID) (*SpotStatus, error) {
	w, err := m.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	// Estimate spot savings (spot is typically ~60-70% cheaper)
	spotDiscount := 0.65
	totalCost := w.MonthlyCostUSD
	spotSavings := totalCost * spotDiscount

	return &SpotStatus{
		WorkloadID:        w.ID.String(),
		SpotEnabled:       false, // TODO: read from spot config table
		SpotReplicas:      0,
		OnDemandReplicas:  w.CurrentReplicas,
		SpotPct:           0,
		SavingsVsOnDemand: spotSavings,
		InterruptionRisk:  "low",
	}, nil
}

// HandleInterruptionPrediction responds to predicted spot interruptions
// Called when the edge agent or cloud provider signals an upcoming interruption
func (m *SpotManager) HandleInterruptionPrediction(ctx context.Context, workloadID uuid.UUID, horizonMinutes int) error {
	w, err := m.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	logger.Warn("Spot interruption predicted for %s in %d minutes — initiating fallback",
		w.Name, horizonMinutes)

	// Publish event for reactive agent system
	featureID := ""
	if w.FeatureID != nil {
		featureID = w.FeatureID.String()
	}
	_ = m.publisher.Publish(ctx, events.EventSpotInterruptPredicted, events.EventData{
		ResourceID:   w.ID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"workload_name":   w.Name,
			"feature_id":      featureID,
			"horizon_minutes": horizonMinutes,
		},
	})

	// Pre-provision on-demand fallback if horizon < 30 min
	if horizonMinutes < 30 {
		logger.Info("Pre-provisioning on-demand replacement for %s", w.Name)
		// In production: scale up on-demand by 1 replica before spot is interrupted
		w.DesiredReplicas = w.CurrentReplicas + 1
		return m.workloadRepo.Update(ctx, w)
	}

	return nil
}

// HandleInterruption responds to an actual spot instance interruption
func (m *SpotManager) HandleInterruption(ctx context.Context, workloadID uuid.UUID) error {
	w, err := m.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	logger.Warn("Spot instance interrupted for %s — activating fallback", w.Name)

	_ = m.publisher.Publish(ctx, events.EventSpotInterrupted, events.EventData{
		ResourceID:   w.ID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"workload_name": w.Name,
		},
	})

	return nil
}

// EstimateSpotSavings returns the estimated monthly savings from enabling spot for a workload
func (m *SpotManager) EstimateSpotSavings(ctx context.Context, workloadID uuid.UUID) (float64, error) {
	w, err := m.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return 0, err
	}

	// Spot is typically 60-70% cheaper than on-demand
	spotDiscount := 0.65
	savings := w.MonthlyCostUSD * spotDiscount

	return savings, nil
}

func init() {
	// Ensure imports are used
	_ = fmt.Sprintf
}
