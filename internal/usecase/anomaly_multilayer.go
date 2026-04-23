package usecase

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/ml"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// MultiLayerAnomalyDetector orchestrates all three anomaly detection layers:
//   - Layer 1: Dynamic thresholds (mean ± 3σ) — fast, always on
//   - Layer 2: Isolation Forest (multivariate) — handles seasonality
//   - Layer 3: Autoencoder (correlation) — detects broken correlations
//
// A high-confidence anomaly requires at least two layers to agree.
type MultiLayerAnomalyDetector struct {
	metricsRepo  *postgres.MetricsRepository
	workloadRepo *postgres.WorkloadRepository

	// Layer 2: trained per-workload
	forests map[uuid.UUID]*ml.IsolationForest

	// Layer 3: trained per-workload
	autoencoders map[uuid.UUID]*ml.Autoencoder
}

func NewMultiLayerAnomalyDetector(
	metricsRepo *postgres.MetricsRepository,
	workloadRepo *postgres.WorkloadRepository,
) *MultiLayerAnomalyDetector {
	return &MultiLayerAnomalyDetector{
		metricsRepo:  metricsRepo,
		workloadRepo: workloadRepo,
		forests:      make(map[uuid.UUID]*ml.IsolationForest),
		autoencoders: make(map[uuid.UUID]*ml.Autoencoder),
	}
}

// TrainModels trains Layer 2+3 models on historical data for a workload.
// Should be called periodically (e.g., daily) or after significant baseline changes.
func (d *MultiLayerAnomalyDetector) TrainModels(ctx context.Context, workloadID uuid.UUID) error {
	since := time.Now().Add(-7 * 24 * time.Hour)
	metrics, err := d.metricsRepo.FindByWorkload(ctx, workloadID, since, 10000)
	if err != nil {
		return err
	}

	if len(metrics) < 100 {
		return nil // not enough data to train
	}

	// Build training data
	var featureVectors [][]float64
	for _, m := range metrics {
		hour := m.Timestamp.Hour()
		day := int(m.Timestamp.Weekday())
		fv := ml.BuildFeatureVector(m.CPUPct, m.MemoryPct, m.RequestsPerSec, m.LatencyP99Ms, m.ErrorRatePct, hour, day)
		featureVectors = append(featureVectors, fv)
	}

	// Train Isolation Forest (Layer 2)
	forest := ml.NewIsolationForest(100, 256)
	forest.Fit(featureVectors)
	d.forests[workloadID] = forest

	// Train Autoencoder (Layer 3) — uses raw metrics (no time features)
	var rawVectors [][]float64
	for _, m := range metrics {
		rawVectors = append(rawVectors, []float64{
			m.CPUPct, m.MemoryPct, m.RequestsPerSec, m.LatencyP99Ms, m.ErrorRatePct,
		})
	}
	ae := ml.NewAutoencoder()
	ae.Fit(rawVectors)
	d.autoencoders[workloadID] = ae

	logger.Info("Anomaly models trained for workload %s (%d samples)", workloadID, len(metrics))
	return nil
}

// Detect runs all three layers and produces a combined anomaly score.
// High-confidence requires at least 2 layers to agree.
func (d *MultiLayerAnomalyDetector) Detect(ctx context.Context, workloadID uuid.UUID) (*domain.AnomalyScore, error) {
	// Get recent metrics for Layer 1
	since := time.Now().Add(-20 * time.Minute)
	metrics, err := d.metricsRepo.FindByWorkload(ctx, workloadID, since, 200)
	if err != nil || len(metrics) < 5 {
		return nil, nil
	}

	latest := metrics[0]
	now := time.Now()

	// Layer 1: Dynamic thresholds
	layer1Score, layer1Desc := d.runLayer1(metrics)

	// Layer 2: Isolation Forest
	layer2Score := d.runLayer2(workloadID, latest, now)

	// Layer 3: Autoencoder (correlation)
	layer3Score, layer3Desc := d.runLayer3(workloadID, latest)

	// Combine: require at least 2 layers to agree for high confidence
	layersTriggered := 0
	if layer1Score > 0.5 {
		layersTriggered++
	}
	if layer2Score > 0.5 {
		layersTriggered++
	}
	if layer3Score > 0.5 {
		layersTriggered++
	}

	if layersTriggered < 1 {
		return nil, nil // no anomaly
	}

	// Combined score: weighted average
	combinedScore := (layer1Score*0.4 + layer2Score*0.35 + layer3Score*0.25)

	severity := domain.AlertSeverityWarning
	if layersTriggered >= 2 && combinedScore > 0.7 {
		severity = domain.AlertSeverityCritical
	}

	confidence := math.Min(1.0, float64(layersTriggered)/2.0)

	// Determine anomaly type
	anomalyType := domain.AnomalyTypeSpike
	if layer3Score > 0.5 {
		anomalyType = domain.AnomalyTypeCorrelationBreak
	}

	// Build description
	desc := layer1Desc
	if layer3Desc != "" {
		desc = layer3Desc
	}

	var affectedMetrics []string
	if latest.CPUPct > 80 {
		affectedMetrics = append(affectedMetrics, "cpu")
	}
	if latest.MemoryPct > 80 {
		affectedMetrics = append(affectedMetrics, "memory")
	}
	if latest.ErrorRatePct > 1 {
		affectedMetrics = append(affectedMetrics, "error_rate")
	}
	if latest.LatencyP99Ms > 500 {
		affectedMetrics = append(affectedMetrics, "latency_p99")
	}

	return &domain.AnomalyScore{
		WorkloadID:      workloadID,
		Score:           combinedScore,
		AnomalyType:     anomalyType,
		Severity:        severity,
		AffectedMetrics: affectedMetrics,
		Confidence:      confidence,
		Description:     desc,
		SuggestedAction: suggestAction(anomalyType, affectedMetrics),
		DetectedAt:      now,
	}, nil
}

func (d *MultiLayerAnomalyDetector) runLayer1(metrics []*domain.WorkloadMetricsSnapshot) (float64, string) {
	if len(metrics) < 5 {
		return 0, ""
	}

	latest := metrics[0]
	var cpuVals, memVals, errVals []float64
	for _, m := range metrics {
		cpuVals = append(cpuVals, m.CPUPct)
		memVals = append(memVals, m.MemoryPct)
		errVals = append(errVals, m.ErrorRatePct)
	}

	maxZScore := 0.0
	desc := ""

	for _, check := range []struct {
		name    string
		current float64
		history []float64
	}{
		{"CPU", latest.CPUPct, cpuVals},
		{"memory", latest.MemoryPct, memVals},
		{"error_rate", latest.ErrorRatePct, errVals},
	} {
		mean, std := meanStddev(check.history)
		if std == 0 {
			continue
		}
		z := math.Abs((check.current - mean) / std)
		if z > maxZScore {
			maxZScore = z
			desc = check.name + " is " + formatZScore(z) + " from mean"
		}
	}

	// Normalize to 0-1 score
	score := math.Min(1.0, maxZScore/5.0)
	return score, desc
}

func (d *MultiLayerAnomalyDetector) runLayer2(workloadID uuid.UUID, m *domain.WorkloadMetricsSnapshot, t time.Time) float64 {
	forest, ok := d.forests[workloadID]
	if !ok {
		return 0 // model not trained yet
	}

	fv := ml.BuildFeatureVector(m.CPUPct, m.MemoryPct, m.RequestsPerSec, m.LatencyP99Ms, m.ErrorRatePct, t.Hour(), int(t.Weekday()))
	return forest.Score(fv)
}

func (d *MultiLayerAnomalyDetector) runLayer3(workloadID uuid.UUID, m *domain.WorkloadMetricsSnapshot) (float64, string) {
	ae, ok := d.autoencoders[workloadID]
	if !ok {
		return 0, ""
	}

	features := []float64{m.CPUPct, m.MemoryPct, m.RequestsPerSec, m.LatencyP99Ms, m.ErrorRatePct}
	score := ae.Score(features)
	desc := ae.DescribeAnomaly(features, []string{"cpu", "memory", "request_rate", "latency_p99", "error_rate"})

	return score, desc
}

func formatZScore(z float64) string {
	if z > 4 {
		return "extremely far (>4σ)"
	}
	if z > 3 {
		return "very far (>3σ)"
	}
	return "moderately far (>2σ)"
}
