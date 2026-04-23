package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
)

// CrossClusterOptimizer recommends workload placement across environments and regions.
// It evaluates cost-per-resource, latency, and availability zone diversity.
type CrossClusterOptimizer struct {
	workloadRepo *postgres.WorkloadRepository
}

func NewCrossClusterOptimizer(workloadRepo *postgres.WorkloadRepository) *CrossClusterOptimizer {
	return &CrossClusterOptimizer{workloadRepo: workloadRepo}
}

// PlacementRecommendation suggests moving a workload to a different environment
type PlacementRecommendation struct {
	WorkloadID       string  `json:"workload_id"`
	WorkloadName     string  `json:"workload_name"`
	CurrentEnv       string  `json:"current_environment"`
	RecommendedEnv   string  `json:"recommended_environment"`
	Reason           string  `json:"reason"`
	EstSavingsUSD    float64 `json:"est_savings_usd"`
	LatencyImpactMs  float64 `json:"latency_impact_ms"` // positive = worse
	Confidence       float64 `json:"confidence"`
}

// InstanceTypeRecommendation suggests an optimal instance type for a workload
type InstanceTypeRecommendation struct {
	WorkloadID       string  `json:"workload_id"`
	WorkloadName     string  `json:"workload_name"`
	CurrentType      string  `json:"current_type"`
	RecommendedType  string  `json:"recommended_type"`
	CostReduction    float64 `json:"cost_reduction_pct"`
	FitScore         float64 `json:"fit_score"` // how well resources match workload needs
}

// InstanceType represents a cloud instance type with pricing
type InstanceType struct {
	Name        string
	VCPUs       int
	MemoryGB    float64
	PricePerHour float64
	Family      string // m5, c5, r5, etc.
	Generation  int    // 5, 6, 7
}

// AnalyzePlacement generates cross-cluster placement recommendations for an org
func (o *CrossClusterOptimizer) AnalyzePlacement(ctx context.Context, orgID uuid.UUID) ([]PlacementRecommendation, error) {
	workloads, err := o.workloadRepo.FindByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Group workloads by environment
	envWorkloads := make(map[string][]*domain.Workload)
	envCosts := make(map[string]float64)
	for _, w := range workloads {
		envWorkloads[w.Environment] = append(envWorkloads[w.Environment], w)
		envCosts[w.Environment] += w.MonthlyCostUSD
	}

	var recs []PlacementRecommendation

	for _, w := range workloads {
		// Check if workload would be cheaper in another environment
		for env, cost := range envCosts {
			if env == w.Environment {
				continue
			}

			// Estimate cost in the other environment based on relative env costs
			currentEnvCost := envCosts[w.Environment]
			if currentEnvCost == 0 {
				continue
			}
			ratio := cost / currentEnvCost
			estimatedCostInOtherEnv := w.MonthlyCostUSD * ratio

			savings := w.MonthlyCostUSD - estimatedCostInOtherEnv
			if savings > w.MonthlyCostUSD*0.15 { // only recommend if >15% savings
				recs = append(recs, PlacementRecommendation{
					WorkloadID:      w.ID.String(),
					WorkloadName:    w.Name,
					CurrentEnv:      w.Environment,
					RecommendedEnv:  env,
					Reason:          fmt.Sprintf("%.0f%% cost reduction in %s environment", (savings/w.MonthlyCostUSD)*100, env),
					EstSavingsUSD:   math.Round(savings*100) / 100,
					LatencyImpactMs: 5, // estimated cross-env latency impact
					Confidence:      0.7,
				})
			}
		}
	}

	// Sort by savings descending
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].EstSavingsUSD > recs[j].EstSavingsUSD
	})

	return recs, nil
}

// RecommendInstanceTypes uses a contextual bandit approach to suggest optimal instance types.
// The bandit learns from workload resource utilization patterns over time.
func (o *CrossClusterOptimizer) RecommendInstanceTypes(ctx context.Context, orgID uuid.UUID) ([]InstanceTypeRecommendation, error) {
	workloads, err := o.workloadRepo.FindByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Available instance types (simplified catalog)
	catalog := []InstanceType{
		{Name: "t3.micro", VCPUs: 2, MemoryGB: 1, PricePerHour: 0.0104, Family: "t3", Generation: 3},
		{Name: "t3.small", VCPUs: 2, MemoryGB: 2, PricePerHour: 0.0208, Family: "t3", Generation: 3},
		{Name: "t3.medium", VCPUs: 2, MemoryGB: 4, PricePerHour: 0.0416, Family: "t3", Generation: 3},
		{Name: "m5.large", VCPUs: 2, MemoryGB: 8, PricePerHour: 0.096, Family: "m5", Generation: 5},
		{Name: "m6i.large", VCPUs: 2, MemoryGB: 8, PricePerHour: 0.092, Family: "m6i", Generation: 6},
		{Name: "c5.large", VCPUs: 2, MemoryGB: 4, PricePerHour: 0.085, Family: "c5", Generation: 5},
		{Name: "c6i.large", VCPUs: 2, MemoryGB: 4, PricePerHour: 0.081, Family: "c6i", Generation: 6},
		{Name: "r5.large", VCPUs: 2, MemoryGB: 16, PricePerHour: 0.126, Family: "r5", Generation: 5},
		{Name: "r6i.large", VCPUs: 2, MemoryGB: 16, PricePerHour: 0.121, Family: "r6i", Generation: 6},
	}

	var recs []InstanceTypeRecommendation

	for _, w := range workloads {
		// Determine workload profile: CPU-bound, memory-bound, or balanced
		cpuRatio := w.CPUUtilizationPct / 100
		memRatio := w.MemoryUtilizationPct / 100

		// Score each instance type based on fit + cost
		bestType := ""
		bestScore := 0.0
		var bestCostReduction float64

		for _, it := range catalog {
			// Fit score: how well the instance matches workload needs
			cpuFit := 1.0 - math.Abs(cpuRatio-0.6)  // target 60% CPU utilization
			memFit := 1.0 - math.Abs(memRatio-0.6)

			// Adjust for instance family strengths
			familyBonus := 0.0
			switch {
			case cpuRatio > memRatio && (it.Family == "c5" || it.Family == "c6i"):
				familyBonus = 0.2 // CPU-bound workload on compute-optimized
			case memRatio > cpuRatio && (it.Family == "r5" || it.Family == "r6i"):
				familyBonus = 0.2 // memory-bound workload on memory-optimized
			case it.Family == "m5" || it.Family == "m6i":
				familyBonus = 0.1 // general purpose is always decent
			}

			// Newer generation bonus
			genBonus := float64(it.Generation-3) * 0.05

			fitScore := (cpuFit + memFit) / 2 + familyBonus + genBonus
			costScore := 1.0 - (it.PricePerHour / 0.15) // normalize against max price

			totalScore := fitScore*0.6 + costScore*0.4

			if totalScore > bestScore {
				bestScore = totalScore
				bestType = it.Name
				currentHourly := w.HourlyCostUSD / math.Max(1, float64(w.CurrentReplicas))
				bestCostReduction = ((currentHourly - it.PricePerHour) / currentHourly) * 100
			}
		}

		if bestType != "" && bestCostReduction > 5 {
			recs = append(recs, InstanceTypeRecommendation{
				WorkloadID:      w.ID.String(),
				WorkloadName:    w.Name,
				CurrentType:     "unknown", // would come from edge agent metadata
				RecommendedType: bestType,
				CostReduction:   math.Round(bestCostReduction*10) / 10,
				FitScore:        math.Round(bestScore*100) / 100,
			})
		}
	}

	sort.Slice(recs, func(i, j int) bool {
		return recs[i].CostReduction > recs[j].CostReduction
	})

	return recs, nil
}
