package ml

import (
	"math"
	"testing"
)

func TestIsolationForest_NormalVsAnomaly(t *testing.T) {
	// Train on normal cluster of data around (0.5, 0.5, ...)
	data := make([][]float64, 500)
	for i := range data {
		x := 0.4 + 0.2*float64(i%10)/10.0
		data[i] = []float64{x, x, x, x}
	}

	f := NewIsolationForest(100, 256)
	f.Fit(data)

	// Normal point should have low anomaly score
	normalScore := f.Score([]float64{0.5, 0.5, 0.5, 0.5})
	if normalScore > 0.6 {
		t.Errorf("normal point scored too high: %.3f (expected <0.6)", normalScore)
	}

	// Anomalous point far from the cluster
	anomalyScore := f.Score([]float64{5.0, 5.0, 5.0, 5.0})
	if anomalyScore < 0.5 {
		t.Errorf("anomaly point scored too low: %.3f (expected >0.5)", anomalyScore)
	}

	// Anomaly should score higher than normal
	if anomalyScore <= normalScore {
		t.Errorf("anomaly (%.3f) should score higher than normal (%.3f)", anomalyScore, normalScore)
	}
}

func TestIsolationForest_EmptyData(t *testing.T) {
	f := NewIsolationForest(100, 256)
	f.Fit([][]float64{})

	score := f.Score([]float64{1.0, 2.0})
	if score != 0 {
		t.Errorf("expected 0 score for untrained forest, got %.3f", score)
	}
}

func TestIsolationForest_DefaultParams(t *testing.T) {
	f := NewIsolationForest(0, 0)
	if f.numTrees != 100 {
		t.Errorf("expected default numTrees=100, got %d", f.numTrees)
	}
	if f.sampleSize != 256 {
		t.Errorf("expected default sampleSize=256, got %d", f.sampleSize)
	}
}

func TestBuildFeatureVector(t *testing.T) {
	fv := BuildFeatureVector(80, 60, 5000, 200, 1.5, 12, 3)

	if len(fv) != 9 {
		t.Fatalf("expected 9 features, got %d", len(fv))
	}

	// cpu: 80/100 = 0.8
	if math.Abs(fv[0]-0.8) > 0.001 {
		t.Errorf("cpu feature: expected 0.8, got %.3f", fv[0])
	}
	// memory: 60/100 = 0.6
	if math.Abs(fv[1]-0.6) > 0.001 {
		t.Errorf("memory feature: expected 0.6, got %.3f", fv[1])
	}

	// Check cyclical encoding: hour=12 should give sin(π) ≈ 0, cos(π) ≈ -1
	if math.Abs(fv[5]) > 0.01 {
		t.Errorf("hourSin for hour=12: expected ~0, got %.3f", fv[5])
	}
	if math.Abs(fv[6]-(-1.0)) > 0.01 {
		t.Errorf("hourCos for hour=12: expected ~-1, got %.3f", fv[6])
	}
}

func TestAvgPathLength(t *testing.T) {
	tests := []struct {
		n        float64
		expected float64
	}{
		{1, 0},
		{2, 1},
		{256, 0}, // just check it's positive
	}

	for _, tc := range tests {
		result := avgPathLength(tc.n)
		if tc.n <= 2 {
			if result != tc.expected {
				t.Errorf("avgPathLength(%.0f) = %.3f, want %.3f", tc.n, result, tc.expected)
			}
		} else {
			if result <= 0 {
				t.Errorf("avgPathLength(%.0f) = %.3f, should be positive", tc.n, result)
			}
		}
	}
}
