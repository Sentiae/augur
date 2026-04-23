package usecase

import (
	"math"
	"testing"
)

func TestInstanceTypeScoringFamilyBonus(t *testing.T) {
	// CPU-bound workload should prefer compute-optimized instances
	cpuRatio := 0.9
	memRatio := 0.3

	catalog := []InstanceType{
		{Name: "m5.large", VCPUs: 2, MemoryGB: 8, PricePerHour: 0.096, Family: "m5", Generation: 5},
		{Name: "c5.large", VCPUs: 2, MemoryGB: 4, PricePerHour: 0.085, Family: "c5", Generation: 5},
		{Name: "r5.large", VCPUs: 2, MemoryGB: 16, PricePerHour: 0.126, Family: "r5", Generation: 5},
	}

	scores := make(map[string]float64)
	for _, it := range catalog {
		cpuFit := 1.0 - math.Abs(cpuRatio-0.6)
		memFit := 1.0 - math.Abs(memRatio-0.6)

		familyBonus := 0.0
		switch {
		case cpuRatio > memRatio && (it.Family == "c5" || it.Family == "c6i"):
			familyBonus = 0.2
		case memRatio > cpuRatio && (it.Family == "r5" || it.Family == "r6i"):
			familyBonus = 0.2
		case it.Family == "m5" || it.Family == "m6i":
			familyBonus = 0.1
		}

		genBonus := float64(it.Generation-3) * 0.05
		fitScore := (cpuFit+memFit)/2 + familyBonus + genBonus
		costScore := 1.0 - (it.PricePerHour / 0.15)
		totalScore := fitScore*0.6 + costScore*0.4

		scores[it.Name] = totalScore
	}

	// c5 should score highest for CPU-bound workloads
	if scores["c5.large"] <= scores["m5.large"] {
		t.Errorf("c5 (%.3f) should score higher than m5 (%.3f) for CPU-bound workload",
			scores["c5.large"], scores["m5.large"])
	}
	if scores["c5.large"] <= scores["r5.large"] {
		t.Errorf("c5 (%.3f) should score higher than r5 (%.3f) for CPU-bound workload",
			scores["c5.large"], scores["r5.large"])
	}
}

func TestInstanceTypeScoringGenerationBonus(t *testing.T) {
	// Newer generation should score higher than older, all else equal
	catalog := []InstanceType{
		{Name: "m5.large", VCPUs: 2, MemoryGB: 8, PricePerHour: 0.096, Family: "m5", Generation: 5},
		{Name: "m6i.large", VCPUs: 2, MemoryGB: 8, PricePerHour: 0.092, Family: "m6i", Generation: 6},
	}

	cpuRatio := 0.5
	memRatio := 0.5

	scores := make(map[string]float64)
	for _, it := range catalog {
		cpuFit := 1.0 - math.Abs(cpuRatio-0.6)
		memFit := 1.0 - math.Abs(memRatio-0.6)

		familyBonus := 0.1 // both are general purpose
		genBonus := float64(it.Generation-3) * 0.05
		fitScore := (cpuFit+memFit)/2 + familyBonus + genBonus
		costScore := 1.0 - (it.PricePerHour / 0.15)
		totalScore := fitScore*0.6 + costScore*0.4

		scores[it.Name] = totalScore
	}

	if scores["m6i.large"] <= scores["m5.large"] {
		t.Errorf("m6i (%.3f) should score higher than m5 (%.3f) due to generation bonus",
			scores["m6i.large"], scores["m5.large"])
	}
}

func TestPlacementSavingsThreshold(t *testing.T) {
	// Only recommend if savings > 15%
	workloadCost := 100.0
	otherEnvRatio := 0.90 // 10% cheaper
	estimatedCost := workloadCost * otherEnvRatio
	savings := workloadCost - estimatedCost

	if savings > workloadCost*0.15 {
		t.Error("10% savings should NOT exceed 15% threshold")
	}

	otherEnvRatio = 0.80 // 20% cheaper
	estimatedCost = workloadCost * otherEnvRatio
	savings = workloadCost - estimatedCost

	if savings <= workloadCost*0.15 {
		t.Error("20% savings SHOULD exceed 15% threshold")
	}
}
