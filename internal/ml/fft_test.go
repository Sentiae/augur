package ml

import (
	"math"
	"testing"
)

func TestFFT_DetectDailyCycle(t *testing.T) {
	// Generate data with a clear 24-hour cycle (1 sample per hour, 7 days)
	n := 168 // 7 * 24
	data := make([]float64, n)
	for i := range data {
		data[i] = 100 + 30*math.Sin(2*math.Pi*float64(i)/24.0) // 24-hour cycle
	}

	analyzer := NewFFTAnalyzer()
	periods := analyzer.DetectPeriods(data, 3)

	if len(periods) == 0 {
		t.Fatal("expected at least one period detected")
	}

	// The strongest period should be approximately 24
	strongest := periods[0]
	if strongest.PeriodPoints < 20 || strongest.PeriodPoints > 28 {
		t.Errorf("strongest period=%d, expected ~24", strongest.PeriodPoints)
	}

	if strongest.Power < 0.5 {
		t.Errorf("strongest power=%.3f, expected >0.5 for pure sinusoid", strongest.Power)
	}
}

func TestFFT_DetectWeeklyCycle(t *testing.T) {
	// Generate data with daily + weekly cycles
	n := 512 // pad-friendly, ~21 days at hourly
	data := make([]float64, n)
	for i := range data {
		daily := 20 * math.Sin(2*math.Pi*float64(i)/24.0)
		weekly := 40 * math.Sin(2*math.Pi*float64(i)/168.0) // 168h = 7 days
		data[i] = 200 + daily + weekly
	}

	analyzer := NewFFTAnalyzer()
	periods := analyzer.DetectPeriods(data, 5)

	if len(periods) < 2 {
		t.Fatalf("expected at least 2 periods, got %d", len(periods))
	}

	// Should detect both ~24h and ~168h cycles
	foundDaily := false
	foundWeekly := false
	for _, p := range periods {
		hours := float64(p.PeriodPoints) // 1 sample per hour
		if hours > 20 && hours < 30 {
			foundDaily = true
		}
		if hours > 140 && hours < 200 {
			foundWeekly = true
		}
	}

	if !foundDaily {
		t.Error("daily cycle not detected")
	}
	if !foundWeekly {
		t.Error("weekly cycle not detected")
	}
}

func TestFFT_DetectSeasonLength(t *testing.T) {
	// Clear daily pattern, 1 sample per hour
	n := 256
	data := make([]float64, n)
	for i := range data {
		data[i] = 50 + 15*math.Sin(2*math.Pi*float64(i)/24.0)
	}

	analyzer := NewFFTAnalyzer()
	seasonLen := analyzer.DetectSeasonLength(data, 1.0) // 1 sample per hour

	// Should detect ~24 points
	if seasonLen < 20 || seasonLen > 30 {
		t.Errorf("detected season length=%d, expected ~24", seasonLen)
	}
}

func TestFFT_NoPattern(t *testing.T) {
	// Flat data with no seasonality
	data := make([]float64, 128)
	for i := range data {
		data[i] = 100.0
	}

	analyzer := NewFFTAnalyzer()
	periods := analyzer.DetectPeriods(data, 3)

	// Should detect nothing significant
	for _, p := range periods {
		if p.Power > 0.1 {
			t.Errorf("detected spurious period %d with power %.3f on flat data", p.PeriodPoints, p.Power)
		}
	}
}

func TestFFT_TooShort(t *testing.T) {
	analyzer := NewFFTAnalyzer()
	periods := analyzer.DetectPeriods([]float64{1, 2, 3}, 3)
	if periods != nil {
		t.Error("expected nil for data too short")
	}
}

func TestNextPow2(t *testing.T) {
	tests := []struct{ in, want int }{
		{1, 1}, {2, 2}, {3, 4}, {5, 8}, {7, 8},
		{8, 8}, {9, 16}, {100, 128}, {256, 256},
	}
	for _, tt := range tests {
		got := nextPow2(tt.in)
		if got != tt.want {
			t.Errorf("nextPow2(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
