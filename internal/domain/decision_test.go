package domain

import (
	"testing"
)

func TestWeightsForMode(t *testing.T) {
	tests := []struct {
		mode         OptimizationMode
		wantCost     float64
		wantPerf     float64
		wantRel      float64
	}{
		{OptimizationModeCost, 0.7, 0.2, 0.1},
		{OptimizationModePerformance, 0.1, 0.7, 0.2},
		{OptimizationModeReliability, 0.1, 0.1, 0.8},
		{OptimizationModeBalanced, 0.34, 0.33, 0.33},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			w := WeightsForMode(tt.mode)
			if w.Cost != tt.wantCost {
				t.Errorf("Cost: got %v, want %v", w.Cost, tt.wantCost)
			}
			if w.Performance != tt.wantPerf {
				t.Errorf("Performance: got %v, want %v", w.Performance, tt.wantPerf)
			}
			if w.Reliability != tt.wantRel {
				t.Errorf("Reliability: got %v, want %v", w.Reliability, tt.wantRel)
			}
		})
	}
}

func TestPolicyWeightsScore(t *testing.T) {
	// Cost mode: prefer cheaper options
	costWeights := WeightsForMode(OptimizationModeCost)

	cheap := ScalingCandidate{TargetReplicas: 2, CostDeltaUSD: -0.10, LatencyImpact: 0.05, ReliabilityGain: 0.0}
	expensive := ScalingCandidate{TargetReplicas: 5, CostDeltaUSD: 0.50, LatencyImpact: -0.10, ReliabilityGain: 0.1}

	cheapScore := costWeights.Score(cheap)
	expensiveScore := costWeights.Score(expensive)

	if cheapScore <= expensiveScore {
		t.Errorf("Cost mode should prefer cheaper option: cheap=%.3f, expensive=%.3f", cheapScore, expensiveScore)
	}

	// Performance mode: prefer better latency
	perfWeights := WeightsForMode(OptimizationModePerformance)

	fastCandidate := ScalingCandidate{TargetReplicas: 5, CostDeltaUSD: 0.50, LatencyImpact: -50, ReliabilityGain: 0.0}
	slowCandidate := ScalingCandidate{TargetReplicas: 2, CostDeltaUSD: -0.10, LatencyImpact: 20, ReliabilityGain: 0.0}

	fastScore := perfWeights.Score(fastCandidate)
	slowScore := perfWeights.Score(slowCandidate)

	if fastScore <= slowScore {
		t.Errorf("Performance mode should prefer faster option: fast=%.3f, slow=%.3f", fastScore, slowScore)
	}
}
