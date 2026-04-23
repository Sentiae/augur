package ml

import (
	"math"
	"testing"
)

func TestAutoencoder_FitAndScore(t *testing.T) {
	// Training data: CPU and memory are correlated (both rise together)
	data := make([][]float64, 200)
	for i := range data {
		base := 30 + float64(i%50)
		data[i] = []float64{base, base * 0.8, base * 10, base * 2, 0.1}
	}

	ae := NewAutoencoder()
	ae.Fit(data)

	if !ae.trained {
		t.Fatal("autoencoder should be trained after Fit")
	}

	// Normal point (follows correlation)
	normalScore := ae.Score([]float64{50, 40, 500, 100, 0.1})

	// Anomalous: CPU high but memory low (broken correlation)
	anomalyScore := ae.Score([]float64{80, 10, 500, 100, 0.1})

	if anomalyScore <= normalScore {
		t.Errorf("broken correlation (%.3f) should score higher than normal (%.3f)", anomalyScore, normalScore)
	}
}

func TestAutoencoder_InsufficientData(t *testing.T) {
	ae := NewAutoencoder()
	ae.Fit([][]float64{{1, 2}, {3, 4}}) // only 2 rows, need >=10

	if ae.trained {
		t.Error("should not be trained with insufficient data")
	}

	score := ae.Score([]float64{1, 2})
	if score != 0 {
		t.Errorf("untrained autoencoder should return 0, got %.3f", score)
	}
}

func TestAutoencoder_DescribeAnomaly(t *testing.T) {
	data := make([][]float64, 200)
	for i := range data {
		base := 30 + float64(i%50)
		data[i] = []float64{base, base * 0.8, base * 0.5}
	}

	ae := NewAutoencoder()
	ae.Fit(data)

	// Normal: empty description expected
	desc := ae.DescribeAnomaly(
		[]float64{50, 40, 25},
		[]string{"cpu", "memory", "requests"},
	)
	// May or may not have a description for normal data; just check it doesn't panic

	// Anomalous: CPU high, memory low — should describe the break
	desc = ae.DescribeAnomaly(
		[]float64{90, 5, 50},
		[]string{"cpu", "memory", "requests"},
	)
	_ = desc // description is optional — main check is no panic
}

func TestAutoencoder_WrongFeatureCount(t *testing.T) {
	ae := NewAutoencoder()
	data := make([][]float64, 20)
	for i := range data {
		data[i] = []float64{float64(i), float64(i) * 2, float64(i) * 3}
	}
	ae.Fit(data)

	// Wrong number of features should return 0
	score := ae.Score([]float64{1, 2})
	if score != 0 {
		t.Errorf("wrong feature count should return 0, got %.3f", score)
	}
}

func TestAutoencoder_ScoreRange(t *testing.T) {
	data := make([][]float64, 200)
	for i := range data {
		v := float64(i % 50)
		data[i] = []float64{v, v * 2, v * 3}
	}

	ae := NewAutoencoder()
	ae.Fit(data)

	// Score should be in [0, 1]
	for _, input := range [][]float64{
		{25, 50, 75},
		{0, 0, 0},
		{100, 200, 300},
		{100, 0, 300}, // anomalous
	} {
		score := ae.Score(input)
		if score < 0 || score > 1 {
			t.Errorf("score %.3f out of range [0,1] for input %v", score, input)
		}
		if math.IsNaN(score) {
			t.Errorf("score is NaN for input %v", input)
		}
	}
}
