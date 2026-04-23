package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// RightsizingEngine analyzes workload resource usage and recommends optimal sizing
type RightsizingEngine struct {
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
}

func NewRightsizingEngine(
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
) *RightsizingEngine {
	return &RightsizingEngine{
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
	}
}

// RightsizingRecommendation contains a rightsizing recommendation for a workload
type RightsizingRecommendation struct {
	WorkloadID              string  `json:"workload_id"`
	WorkloadName            string  `json:"workload_name"`
	CurrentCPURequest       float64 `json:"current_cpu_request_pct"`
	RecommendedCPURequest   float64 `json:"recommended_cpu_request_pct"`
	CurrentMemoryRequest    float64 `json:"current_memory_request_pct"`
	RecommendedMemoryRequest float64 `json:"recommended_memory_request_pct"`
	CurrentReplicas         int     `json:"current_replicas"`
	RecommendedReplicas     int     `json:"recommended_replicas"`
	CurrentMonthlyCost      float64 `json:"current_monthly_cost_usd"`
	RecommendedMonthlyCost  float64 `json:"recommended_monthly_cost_usd"`
	EstimatedSavingsUSD     float64 `json:"estimated_savings_usd"`
	ObservationDays         int     `json:"observation_days"`
	Confidence              float64 `json:"confidence"`
}

// GetRecommendation generates a rightsizing recommendation for a single workload
func (e *RightsizingEngine) GetRecommendation(ctx context.Context, workloadID uuid.UUID) (*RightsizingRecommendation, error) {
	w, err := e.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	// Get 14 days of metrics
	since := time.Now().Add(-14 * 24 * time.Hour)
	metrics, err := e.metricsRepo.FindByWorkload(ctx, workloadID, since, 10000)
	if err != nil {
		return nil, err
	}

	if len(metrics) < 100 {
		return nil, fmt.Errorf("not enough data for rightsizing (need 100+ samples, have %d)", len(metrics))
	}

	// Compute percentiles for CPU and memory
	var cpuVals, memVals []float64
	for _, m := range metrics {
		cpuVals = append(cpuVals, m.CPUPct)
		memVals = append(memVals, m.MemoryPct)
	}

	// CPU: target P50-P70 (allow burst headroom)
	cpuP70 := percentile(cpuVals, 70)
	// Memory: target P95 (avoid OOMKills)
	memP95 := percentile(memVals, 95)

	// Compute recommended replicas based on target utilization
	// If current CPU at P70 is 30% with 5 replicas, we could use 3 replicas at 50% each
	currentAvgCPU := percentile(cpuVals, 50)
	targetCPUUtilization := 60.0 // target 60% CPU utilization

	recommendedReplicas := w.CurrentReplicas
	if currentAvgCPU > 0 && targetCPUUtilization > 0 {
		idealReplicas := math.Ceil(float64(w.CurrentReplicas) * currentAvgCPU / targetCPUUtilization)
		recommendedReplicas = int(math.Max(float64(w.MinReplicas), idealReplicas))
	}

	// Estimate cost
	currentMonthlyCost := w.MonthlyCostUSD
	costPerReplica := currentMonthlyCost / math.Max(1, float64(w.CurrentReplicas))
	recommendedMonthlyCost := costPerReplica * float64(recommendedReplicas)
	savings := currentMonthlyCost - recommendedMonthlyCost

	observationDays := int(time.Since(metrics[len(metrics)-1].Timestamp).Hours() / 24)
	confidence := math.Min(1.0, float64(observationDays)/14.0) // 14 days = full confidence

	return &RightsizingRecommendation{
		WorkloadID:               w.ID.String(),
		WorkloadName:             w.Name,
		CurrentCPURequest:        currentAvgCPU,
		RecommendedCPURequest:    cpuP70,
		CurrentMemoryRequest:     percentile(memVals, 50),
		RecommendedMemoryRequest: memP95,
		CurrentReplicas:          w.CurrentReplicas,
		RecommendedReplicas:      recommendedReplicas,
		CurrentMonthlyCost:       currentMonthlyCost,
		RecommendedMonthlyCost:   recommendedMonthlyCost,
		EstimatedSavingsUSD:      savings,
		ObservationDays:          observationDays,
		Confidence:               confidence,
	}, nil
}

// GetRecommendationsForOrg generates recommendations for all workloads in an org
func (e *RightsizingEngine) GetRecommendationsForOrg(ctx context.Context, orgID uuid.UUID) ([]RightsizingRecommendation, error) {
	workloads, err := e.workloadRepo.FindByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var recommendations []RightsizingRecommendation
	for _, w := range workloads {
		rec, err := e.GetRecommendation(ctx, w.ID)
		if err != nil {
			logger.Debug("Rightsizing skipped for %s: %v", w.Name, err)
			continue
		}
		if rec.EstimatedSavingsUSD > 5 { // only include meaningful savings
			recommendations = append(recommendations, *rec)
		}
	}

	// Sort by savings descending
	sort.Slice(recommendations, func(i, j int) bool {
		return recommendations[i].EstimatedSavingsUSD > recommendations[j].EstimatedSavingsUSD
	})

	return recommendations, nil
}

// ApplyRecommendation applies a rightsizing recommendation to a workload
func (e *RightsizingEngine) ApplyRecommendation(ctx context.Context, workloadID uuid.UUID) (*RightsizingRecommendation, error) {
	rec, err := e.GetRecommendation(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	w, err := e.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	// Apply the recommended replica count
	w.DesiredReplicas = rec.RecommendedReplicas
	w.MinReplicas = int(math.Max(1, float64(rec.RecommendedReplicas-1)))

	if err := e.workloadRepo.Update(ctx, w); err != nil {
		return nil, err
	}

	logger.Info("Rightsizing applied: %s %d→%d replicas (saving $%.2f/mo)",
		w.Name, rec.CurrentReplicas, rec.RecommendedReplicas, rec.EstimatedSavingsUSD)

	return rec, nil
}

// percentile computes the p-th percentile of a sorted float64 slice
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)

	idx := (p / 100) * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))

	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}

	// Linear interpolation
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
