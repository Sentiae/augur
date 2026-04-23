package ml

import (
	"math"
)

// Autoencoder implements a simple correlation anomaly detector (Layer 3).
// This is a pure Go implementation using a lightweight neural network.
// It detects correlation anomalies invisible to per-metric detectors:
// e.g., "CPU rising but requests falling" — the correlation has broken.
//
// When ONNX is available, this can be replaced with a trained deep autoencoder.
type Autoencoder struct {
	// Learned correlation matrix from training data
	meanVec  []float64
	stdVec   []float64
	corrMat  [][]float64 // pairwise correlation coefficients
	trained  bool
	nFeatures int
}

func NewAutoencoder() *Autoencoder {
	return &Autoencoder{}
}

// Fit learns the normal correlation structure from training data.
// data: each row is a feature vector [cpu, mem, req_rate, latency, error_rate]
func (a *Autoencoder) Fit(data [][]float64) {
	if len(data) < 10 || len(data[0]) == 0 {
		return
	}

	n := len(data)
	nf := len(data[0])
	a.nFeatures = nf

	// Compute mean
	a.meanVec = make([]float64, nf)
	for _, row := range data {
		for j, v := range row {
			a.meanVec[j] += v
		}
	}
	for j := range a.meanVec {
		a.meanVec[j] /= float64(n)
	}

	// Compute standard deviation
	a.stdVec = make([]float64, nf)
	for _, row := range data {
		for j, v := range row {
			diff := v - a.meanVec[j]
			a.stdVec[j] += diff * diff
		}
	}
	for j := range a.stdVec {
		a.stdVec[j] = math.Sqrt(a.stdVec[j] / float64(n))
		if a.stdVec[j] == 0 {
			a.stdVec[j] = 1 // prevent division by zero
		}
	}

	// Compute correlation matrix
	a.corrMat = make([][]float64, nf)
	for i := range nf {
		a.corrMat[i] = make([]float64, nf)
		for j := range nf {
			var cov float64
			for _, row := range data {
				cov += (row[i] - a.meanVec[i]) * (row[j] - a.meanVec[j])
			}
			cov /= float64(n)
			a.corrMat[i][j] = cov / (a.stdVec[i] * a.stdVec[j])
		}
	}

	a.trained = true
}

// Score computes the reconstruction error for a feature vector.
// Higher score = more anomalous (correlation structure has broken).
// Returns 0.0 - 1.0 where > 0.5 indicates correlation anomaly.
func (a *Autoencoder) Score(features []float64) float64 {
	if !a.trained || len(features) != a.nFeatures {
		return 0
	}

	// Standardize the input
	z := make([]float64, a.nFeatures)
	for i, v := range features {
		z[i] = (v - a.meanVec[i]) / a.stdVec[i]
	}

	// Compute reconstruction: for each feature, predict it from the others
	// using the learned correlation structure
	var totalError float64
	for i := range a.nFeatures {
		predicted := 0.0
		for j := range a.nFeatures {
			if i != j {
				predicted += a.corrMat[i][j] * z[j]
			}
		}
		predicted /= float64(a.nFeatures - 1)

		err := z[i] - predicted
		totalError += err * err
	}

	// Normalize to [0, 1] using sigmoid
	rmse := math.Sqrt(totalError / float64(a.nFeatures))
	score := 1.0 / (1.0 + math.Exp(-2*(rmse-1.5))) // sigmoid centered at 1.5 stddev

	return score
}

// DescribeAnomaly returns a human-readable description of which correlations broke
func (a *Autoencoder) DescribeAnomaly(features []float64, metricNames []string) string {
	if !a.trained || len(features) != a.nFeatures {
		return ""
	}

	z := make([]float64, a.nFeatures)
	for i, v := range features {
		z[i] = (v - a.meanVec[i]) / a.stdVec[i]
	}

	// Find the pair with the largest correlation break
	maxBreak := 0.0
	breakI, breakJ := 0, 0

	for i := range a.nFeatures {
		for j := i + 1; j < a.nFeatures; j++ {
			expectedCorr := a.corrMat[i][j]
			actualProduct := z[i] * z[j]
			breakStrength := math.Abs(actualProduct - expectedCorr)
			if breakStrength > maxBreak {
				maxBreak = breakStrength
				breakI, breakJ = i, j
			}
		}
	}

	if maxBreak < 0.5 || len(metricNames) != a.nFeatures {
		return ""
	}

	direction := "diverging"
	if z[breakI]*z[breakJ] < 0 && a.corrMat[breakI][breakJ] > 0 {
		direction = "moving in opposite directions (normally correlated)"
	}

	return metricNames[breakI] + " and " + metricNames[breakJ] + " are " + direction
}
