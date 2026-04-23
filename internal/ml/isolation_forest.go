package ml

import (
	"math"
	"math/rand"
)

// IsolationForest implements a streaming isolation forest for multivariate anomaly detection.
// This is a pure Go implementation (Layer 2) that handles seasonality via time-of-day features.
// When ONNX is available, this can be replaced with a trained ONNX model.
type IsolationForest struct {
	trees       []*iTree
	numTrees    int
	sampleSize  int
	maxDepth    int
	avgPathLen  float64 // expected average path length for normalization
}

type iTree struct {
	root *iNode
}

type iNode struct {
	splitFeature int
	splitValue   float64
	left, right  *iNode
	size         int // number of samples that reached this node during training
	isLeaf       bool
}

// NewIsolationForest creates a new isolation forest with default parameters.
// numTrees: 100 is standard. sampleSize: 256 is the default from the original paper.
func NewIsolationForest(numTrees, sampleSize int) *IsolationForest {
	if numTrees <= 0 {
		numTrees = 100
	}
	if sampleSize <= 0 {
		sampleSize = 256
	}
	maxDepth := int(math.Ceil(math.Log2(float64(sampleSize))))

	return &IsolationForest{
		numTrees:   numTrees,
		sampleSize: sampleSize,
		maxDepth:   maxDepth,
		avgPathLen: avgPathLength(float64(sampleSize)),
	}
}

// Fit trains the isolation forest on the given data.
// data: [][]float64 where each inner slice is a feature vector.
func (f *IsolationForest) Fit(data [][]float64) {
	if len(data) == 0 {
		return
	}

	f.trees = make([]*iTree, f.numTrees)
	for i := range f.numTrees {
		// Random subsample
		sample := subsample(data, f.sampleSize)
		f.trees[i] = &iTree{root: buildTree(sample, 0, f.maxDepth)}
	}
}

// Score returns the anomaly score for a feature vector.
// Score ranges from 0 (normal) to 1 (anomalous).
// Score > 0.5 indicates anomaly; > 0.7 is high confidence.
func (f *IsolationForest) Score(features []float64) float64 {
	if len(f.trees) == 0 {
		return 0
	}

	avgPath := 0.0
	for _, tree := range f.trees {
		avgPath += pathLength(features, tree.root, 0)
	}
	avgPath /= float64(len(f.trees))

	// Anomaly score: s(x, n) = 2^(-E(h(x)) / c(n))
	score := math.Pow(2, -avgPath/f.avgPathLen)
	return score
}

// BuildFeatureVector constructs a feature vector from workload metrics + time features
func BuildFeatureVector(cpu, memory, requestRate, latencyP99, errorRate float64, hourOfDay, dayOfWeek int) []float64 {
	// Normalize time to cyclical features using sin/cos encoding
	hourSin := math.Sin(2 * math.Pi * float64(hourOfDay) / 24.0)
	hourCos := math.Cos(2 * math.Pi * float64(hourOfDay) / 24.0)
	daySin := math.Sin(2 * math.Pi * float64(dayOfWeek) / 7.0)
	dayCos := math.Cos(2 * math.Pi * float64(dayOfWeek) / 7.0)

	return []float64{
		cpu / 100.0,          // normalize to [0,1]
		memory / 100.0,
		requestRate / 10000.0, // normalize assuming max ~10k rps
		latencyP99 / 1000.0,  // normalize assuming max ~1000ms
		errorRate / 10.0,     // normalize assuming max ~10%
		hourSin, hourCos,
		daySin, dayCos,
	}
}

func buildTree(data [][]float64, depth, maxDepth int) *iNode {
	n := len(data)
	if n <= 1 || depth >= maxDepth {
		return &iNode{size: n, isLeaf: true}
	}

	numFeatures := len(data[0])
	splitFeature := rand.Intn(numFeatures)

	// Find min/max for the selected feature
	minVal, maxVal := data[0][splitFeature], data[0][splitFeature]
	for _, row := range data[1:] {
		if row[splitFeature] < minVal {
			minVal = row[splitFeature]
		}
		if row[splitFeature] > maxVal {
			maxVal = row[splitFeature]
		}
	}

	if minVal == maxVal {
		return &iNode{size: n, isLeaf: true}
	}

	splitValue := minVal + rand.Float64()*(maxVal-minVal)

	var left, right [][]float64
	for _, row := range data {
		if row[splitFeature] < splitValue {
			left = append(left, row)
		} else {
			right = append(right, row)
		}
	}

	return &iNode{
		splitFeature: splitFeature,
		splitValue:   splitValue,
		left:         buildTree(left, depth+1, maxDepth),
		right:        buildTree(right, depth+1, maxDepth),
		size:         n,
	}
}

func pathLength(features []float64, node *iNode, depth float64) float64 {
	if node == nil || node.isLeaf {
		if node != nil && node.size > 1 {
			return depth + avgPathLength(float64(node.size))
		}
		return depth
	}

	if features[node.splitFeature] < node.splitValue {
		return pathLength(features, node.left, depth+1)
	}
	return pathLength(features, node.right, depth+1)
}

// avgPathLength computes the average path length of unsuccessful search in BST
// This is the normalization factor c(n) from the Isolation Forest paper.
func avgPathLength(n float64) float64 {
	if n <= 1 {
		return 0
	}
	if n == 2 {
		return 1
	}
	// H(n-1) ≈ ln(n-1) + Euler-Mascheroni constant
	return 2*(math.Log(n-1)+0.5772156649) - 2*(n-1)/n
}

func subsample(data [][]float64, size int) [][]float64 {
	if len(data) <= size {
		return data
	}
	indices := rand.Perm(len(data))[:size]
	sample := make([][]float64, size)
	for i, idx := range indices {
		sample[i] = data[idx]
	}
	return sample
}
