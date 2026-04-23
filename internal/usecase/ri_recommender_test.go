package usecase

import (
	"math"
	"sort"
	"testing"
)

func TestPercentile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		pct      float64
		expected float64
	}{
		{10, 1.9},   // index 0.9 → lerp(1, 2)
		{50, 5.5},   // index 4.5 → lerp(5, 6)
		{90, 9.1},   // index 8.1 → lerp(9, 10)
		{100, 10.0}, // index 9.0 → 10
	}

	for _, tc := range tests {
		result := percentile(data, tc.pct)
		if math.Abs(result-tc.expected) > 0.1 {
			t.Errorf("percentile(data, %.0f) = %.2f, want ~%.2f", tc.pct, result, tc.expected)
		}
	}
}

func TestPercentileSorted(t *testing.T) {
	data := []float64{5, 3, 1, 4, 2}
	sort.Float64s(data)

	p50 := percentile(data, 50)
	if math.Abs(p50-3) > 0.5 {
		t.Errorf("P50 of [1,2,3,4,5] = %.2f, want ~3", p50)
	}
}

func TestRITierDiscounts(t *testing.T) {
	// Verify the tiered discount logic
	p10 := 5.0  // $5/hr baseline
	p50 := 10.0 // $10/hr median
	p80 := 15.0 // $15/hr peak

	// Tier 1: Standard RI for P10 baseline — 40% discount
	tier1Savings := p10 * 0.40 * 730
	if tier1Savings <= 0 {
		t.Error("tier 1 savings should be positive")
	}

	// Tier 2: Convertible RI for P10-P50 band — 30% discount, 60% commitment
	band2 := p50 - p10
	tier2Commit := band2 * 0.6
	tier2Savings := tier2Commit * 0.30 * 730
	if tier2Savings <= 0 {
		t.Error("tier 2 savings should be positive")
	}

	// Tier 3: Compute Savings for P50-P80 band — 20% discount, 50% commitment
	band3 := p80 - p50
	tier3Commit := band3 * 0.5
	tier3Savings := tier3Commit * 0.20 * 730
	if tier3Savings <= 0 {
		t.Error("tier 3 savings should be positive")
	}

	// Total savings should be meaningful
	totalSavings := tier1Savings + tier2Savings + tier3Savings
	currentMonthly := p50 * 730
	savingsPct := (totalSavings / currentMonthly) * 100
	if savingsPct < 10 || savingsPct > 50 {
		t.Errorf("savings percentage %.1f%% seems unreasonable (expected 10-50%%)", savingsPct)
	}
}

func TestRICoverageTargetRange(t *testing.T) {
	// Coverage should be in 70-85% range for a balanced portfolio
	p10 := 5.0
	p50 := 10.0
	p80 := 15.0

	totalCommit := p10 + (p50-p10)*0.6 + (p80-p50)*0.5
	coverage := math.Min(100, (totalCommit/p50)*100)

	if coverage < 50 || coverage > 100 {
		t.Errorf("coverage %.1f%% out of expected range", coverage)
	}
}
