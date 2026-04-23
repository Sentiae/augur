package usecase

import (
	"math"
	"testing"
)

func TestMeanStddev(t *testing.T) {
	vals := []float64{10, 10, 10, 10, 10}
	mean, stddev := meanStddev(vals)

	if mean != 10.0 {
		t.Errorf("mean: got %f, want 10.0", mean)
	}
	if stddev != 0.0 {
		t.Errorf("stddev: got %f, want 0.0", stddev)
	}

	vals2 := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	mean2, stddev2 := meanStddev(vals2)

	expectedMean := 5.0
	if math.Abs(mean2-expectedMean) > 0.01 {
		t.Errorf("mean: got %f, want %f", mean2, expectedMean)
	}

	expectedStddev := 2.0
	if math.Abs(stddev2-expectedStddev) > 0.1 {
		t.Errorf("stddev: got %f, want ~%f", stddev2, expectedStddev)
	}
}

func TestMeanStddev_Empty(t *testing.T) {
	mean, stddev := meanStddev(nil)
	if mean != 0 || stddev != 0 {
		t.Errorf("empty: got mean=%f, stddev=%f, want 0,0", mean, stddev)
	}
}

func TestCheckMetric_NoAnomaly(t *testing.T) {
	history := []float64{50, 51, 49, 50, 52, 48, 50, 51}
	anomalies := checkMetric("cpu", 50.5, history)

	if len(anomalies) > 0 {
		t.Errorf("expected no anomaly for normal value, got %d", len(anomalies))
	}
}

func TestCheckMetric_Spike(t *testing.T) {
	history := []float64{50, 51, 49, 50, 52, 48, 50, 51}
	// Value far above mean ± 3σ
	anomalies := checkMetric("cpu", 100, history)

	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly for spike, got %d", len(anomalies))
	}
	if anomalies[0].zScore <= 3 {
		t.Errorf("expected z-score > 3 for spike, got %f", anomalies[0].zScore)
	}
}

func TestCheckMetric_Drop(t *testing.T) {
	history := []float64{50, 51, 49, 50, 52, 48, 50, 51}
	anomalies := checkMetric("requests", 0, history)

	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly for drop, got %d", len(anomalies))
	}
	if anomalies[0].zScore >= -3 {
		t.Errorf("expected z-score < -3 for drop, got %f", anomalies[0].zScore)
	}
}

func TestCheckMetric_ZeroStddev(t *testing.T) {
	history := []float64{50, 50, 50, 50}
	anomalies := checkMetric("cpu", 60, history)

	// With zero stddev, should return no anomalies (can't compute z-score)
	if len(anomalies) > 0 {
		t.Errorf("expected no anomaly with zero stddev, got %d", len(anomalies))
	}
}
