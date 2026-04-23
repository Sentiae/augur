package ml

import (
	"math"
	"testing"
)

func TestHoltWinters_FitAndForecast(t *testing.T) {
	// Generate synthetic data: linear trend + seasonal pattern
	seasonLen := 24 // 24 data points per cycle (like hourly data with daily cycle)
	data := make([]float64, 3*seasonLen) // 3 full seasons

	for i := range data {
		trend := float64(i) * 0.5         // gentle upward trend
		seasonal := 10 * math.Sin(2*math.Pi*float64(i%seasonLen)/float64(seasonLen))
		data[i] = 100 + trend + seasonal
	}

	hw := NewHoltWinters(seasonLen)
	if err := hw.Fit(data); err != nil {
		t.Fatalf("Fit failed: %v", err)
	}

	forecasts, err := hw.Forecast(seasonLen)
	if err != nil {
		t.Fatalf("Forecast failed: %v", err)
	}

	if len(forecasts) != seasonLen {
		t.Fatalf("expected %d forecasts, got %d", seasonLen, len(forecasts))
	}

	// Forecasts should be in a reasonable range (100 + trend at end of data ≈ 136 ± seasonal ≈ 10)
	for i, f := range forecasts {
		if f < 50 || f > 200 {
			t.Errorf("forecast[%d] = %.2f, out of reasonable range [50, 200]", i, f)
		}
	}

	// The forecast should show the seasonal pattern (not flat)
	minF, maxF := forecasts[0], forecasts[0]
	for _, f := range forecasts[1:] {
		if f < minF { minF = f }
		if f > maxF { maxF = f }
	}
	seasonalRange := maxF - minF
	if seasonalRange < 5 {
		t.Errorf("forecast seasonal range too small: %.2f (expected >5)", seasonalRange)
	}
}

func TestHoltWinters_ForecastWithIntervals(t *testing.T) {
	seasonLen := 12
	data := make([]float64, 4*seasonLen)
	for i := range data {
		data[i] = 50 + 5*math.Sin(2*math.Pi*float64(i)/float64(seasonLen))
	}

	hw := NewHoltWinters(seasonLen)
	hw.Fit(data)

	residStd := hw.ComputeResidualStddev(data)
	p10, p50, p90, err := hw.ForecastWithIntervals(12, residStd)
	if err != nil {
		t.Fatalf("ForecastWithIntervals failed: %v", err)
	}

	for i := range p50 {
		if p10[i] > p50[i] {
			t.Errorf("step %d: P10 (%.2f) > P50 (%.2f)", i, p10[i], p50[i])
		}
		if p50[i] > p90[i] {
			t.Errorf("step %d: P50 (%.2f) > P90 (%.2f)", i, p50[i], p90[i])
		}
		if p10[i] < 0 {
			t.Errorf("step %d: P10 (%.2f) is negative", i, p10[i])
		}
	}

	// Intervals should widen over time
	interval0 := p90[0] - p10[0]
	intervalLast := p90[len(p90)-1] - p10[len(p10)-1]
	if intervalLast <= interval0 {
		t.Errorf("intervals should widen: step 0 width=%.2f, last step width=%.2f", interval0, intervalLast)
	}
}

func TestHoltWinters_InsufficientData(t *testing.T) {
	hw := NewHoltWinters(24)
	err := hw.Fit([]float64{1, 2, 3})
	if err == nil {
		t.Error("expected error for insufficient data")
	}
}

func TestHoltWinters_NonNegative(t *testing.T) {
	seasonLen := 6
	// Data that trends toward zero
	data := make([]float64, 3*seasonLen)
	for i := range data {
		data[i] = math.Max(0, 10-float64(i)*0.8)
	}

	hw := NewHoltWinters(seasonLen)
	hw.Fit(data)
	forecasts, _ := hw.Forecast(12)

	for i, f := range forecasts {
		if f < 0 {
			t.Errorf("forecast[%d] = %.2f, should not be negative", i, f)
		}
	}
}
