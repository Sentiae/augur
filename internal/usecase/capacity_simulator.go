package usecase

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
)

// CapacitySimulator runs what-if simulations for workload scaling
type CapacitySimulator struct {
	workloadRepo     *postgres.WorkloadRepository
	predictionEngine *PredictionEngine
}

func NewCapacitySimulator(
	workloadRepo *postgres.WorkloadRepository,
	predictionEngine *PredictionEngine,
) *CapacitySimulator {
	return &CapacitySimulator{
		workloadRepo:     workloadRepo,
		predictionEngine: predictionEngine,
	}
}

// SimulationInput defines the simulation scenario
type SimulationInput struct {
	WorkloadID    *uuid.UUID `json:"workload_id,omitempty"`
	Scenario      string     `json:"scenario"` // "2x_traffic", "3x_traffic", "black_friday", "custom"
	DurationHours int        `json:"duration_hours"`
	TrafficMultiplier float64 `json:"traffic_multiplier,omitempty"` // for custom scenario
}

// SimulationResult contains the simulation output
type SimulationResult struct {
	Scenario        string                 `json:"scenario"`
	WorkloadID      string                 `json:"workload_id,omitempty"`
	ScalingPlan     []SimulationScaleStep  `json:"scaling_plan"`
	PeakReplicas    int                    `json:"peak_replicas"`
	EstimatedCostUSD float64              `json:"estimated_cost_usd"`
	SLOProjection   float64                `json:"slo_projection"` // expected compliance %
	Bottlenecks     []string               `json:"bottlenecks"`
	Recommendation  string                 `json:"recommendation"`
}

type SimulationScaleStep struct {
	HourOffset  int     `json:"hour_offset"`
	Replicas    int     `json:"replicas"`
	CPUPct      float64 `json:"cpu_pct"`
	CostPerHour float64 `json:"cost_per_hour"`
	Reason      string  `json:"reason"`
}

// Simulate runs a capacity simulation
func (s *CapacitySimulator) Simulate(ctx context.Context, input SimulationInput) (*SimulationResult, error) {
	multiplier := input.TrafficMultiplier
	if multiplier <= 0 {
		switch input.Scenario {
		case "2x_traffic":
			multiplier = 2.0
		case "3x_traffic":
			multiplier = 3.0
		case "black_friday":
			multiplier = 5.0
		default:
			multiplier = 2.0
		}
	}

	duration := input.DurationHours
	if duration <= 0 {
		duration = 6
	}

	result := &SimulationResult{
		Scenario: input.Scenario,
	}

	// If workload specified, simulate against it
	if input.WorkloadID != nil {
		return s.simulateWorkload(ctx, *input.WorkloadID, multiplier, duration, result)
	}

	// Generic simulation
	result.Recommendation = fmt.Sprintf("Generic simulation: %.0fx traffic for %d hours would require proportional scaling", multiplier, duration)
	return result, nil
}

func (s *CapacitySimulator) simulateWorkload(ctx context.Context, workloadID uuid.UUID, multiplier float64, durationHours int, result *SimulationResult) (*SimulationResult, error) {
	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	result.WorkloadID = w.ID.String()

	currentReplicas := w.CurrentReplicas
	if currentReplicas < 1 {
		currentReplicas = 1
	}

	// Current load per replica
	loadPerReplica := w.CPUUtilizationPct / float64(currentReplicas)
	if loadPerReplica <= 0 {
		loadPerReplica = 20 // assume 20% per replica if unknown
	}

	costPerReplica := w.HourlyCostUSD / float64(currentReplicas)
	if costPerReplica <= 0 {
		costPerReplica = 0.05
	}

	maxReplicas := w.MaxReplicas
	peakReplicas := currentReplicas
	totalCost := 0.0
	var bottlenecks []string

	// Simulate hour by hour
	plan := make([]SimulationScaleStep, durationHours)
	for h := range durationHours {
		// Traffic ramps up linearly to peak at midpoint, then back down
		midpoint := float64(durationHours) / 2
		hourFactor := 1.0
		if float64(h) < midpoint {
			hourFactor = 1.0 + (multiplier-1.0)*(float64(h)/midpoint)
		} else {
			hourFactor = multiplier - (multiplier-1.0)*((float64(h)-midpoint)/midpoint)
		}

		// Required replicas at this load
		totalCPUNeeded := loadPerReplica * float64(currentReplicas) * hourFactor
		targetCPU := 60.0 // target 60% utilization
		neededReplicas := int(math.Ceil(totalCPUNeeded / targetCPU))
		if neededReplicas < 1 {
			neededReplicas = 1
		}

		cpuPct := totalCPUNeeded / float64(neededReplicas)
		hourlyCost := costPerReplica * float64(neededReplicas)
		totalCost += hourlyCost

		if neededReplicas > peakReplicas {
			peakReplicas = neededReplicas
		}

		reason := "normal"
		if neededReplicas > maxReplicas {
			bottlenecks = append(bottlenecks, fmt.Sprintf("Hour %d: needs %d replicas but max is %d", h, neededReplicas, maxReplicas))
			neededReplicas = maxReplicas
			cpuPct = totalCPUNeeded / float64(neededReplicas)
			reason = "capped at max_replicas"
		}

		plan[h] = SimulationScaleStep{
			HourOffset:  h,
			Replicas:    neededReplicas,
			CPUPct:      math.Round(cpuPct*10) / 10,
			CostPerHour: math.Round(hourlyCost*100) / 100,
			Reason:      reason,
		}
	}

	// SLO projection: if any hour exceeds 85% CPU, SLO is at risk
	sloProjection := 99.9
	for _, step := range plan {
		if step.CPUPct > 85 {
			sloProjection = math.Max(95.0, sloProjection-0.5*(step.CPUPct-85))
		}
	}

	result.ScalingPlan = plan
	result.PeakReplicas = peakReplicas
	result.EstimatedCostUSD = math.Round(totalCost*100) / 100
	result.SLOProjection = math.Round(sloProjection*10) / 10
	result.Bottlenecks = bottlenecks

	if peakReplicas > maxReplicas {
		result.Recommendation = fmt.Sprintf("Current max_replicas (%d) is insufficient for %.0fx traffic. Raise to %d to maintain SLO.", maxReplicas, multiplier, peakReplicas)
	} else if sloProjection < 99.0 {
		result.Recommendation = fmt.Sprintf("SLO at risk (%.1f%%) under %.0fx traffic. Consider pre-scaling to %d replicas.", sloProjection, multiplier, peakReplicas)
	} else {
		result.Recommendation = fmt.Sprintf("Infrastructure can handle %.0fx traffic spike. Peak: %d replicas, est. cost: $%.2f", multiplier, peakReplicas, totalCost)
	}

	return result, nil
}

func init() {
	_ = domain.WorkloadTypeFirecracker // ensure domain import is used
}
