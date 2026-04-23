package usecase

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// AnomalyDetector implements Layer 1 anomaly detection (dynamic thresholds)
type AnomalyDetector struct {
	metricsRepo  *postgres.MetricsRepository
	workloadRepo *postgres.WorkloadRepository
	alertRepo    *postgres.AlertRepository
	publisher    events.EventPublisher
}

func NewAnomalyDetector(
	metricsRepo *postgres.MetricsRepository,
	workloadRepo *postgres.WorkloadRepository,
	alertRepo *postgres.AlertRepository,
	publisher events.EventPublisher,
) *AnomalyDetector {
	return &AnomalyDetector{
		metricsRepo:  metricsRepo,
		workloadRepo: workloadRepo,
		alertRepo:    alertRepo,
		publisher:    publisher,
	}
}

// Detect runs Layer 1 anomaly detection for a workload using dynamic thresholds (mean ± 3σ)
func (d *AnomalyDetector) Detect(ctx context.Context, workloadID uuid.UUID) (*domain.AnomalyScore, error) {
	// Get last 20 minutes of metrics
	since := time.Now().Add(-20 * time.Minute)
	metrics, err := d.metricsRepo.FindByWorkload(ctx, workloadID, since, 200)
	if err != nil {
		return nil, err
	}

	if len(metrics) < 5 {
		return nil, nil // not enough data
	}

	// Compute mean and stddev for each metric
	var cpuVals, memVals, reqVals, latVals, errVals []float64
	for _, m := range metrics {
		cpuVals = append(cpuVals, m.CPUPct)
		memVals = append(memVals, m.MemoryPct)
		reqVals = append(reqVals, m.RequestsPerSec)
		latVals = append(latVals, m.LatencyP99Ms)
		errVals = append(errVals, m.ErrorRatePct)
	}

	// Get the latest data point
	latest := metrics[0]

	// Check each metric against dynamic thresholds
	var anomalies []metricAnomaly
	anomalies = append(anomalies, checkMetric("cpu", latest.CPUPct, cpuVals)...)
	anomalies = append(anomalies, checkMetric("memory", latest.MemoryPct, memVals)...)
	anomalies = append(anomalies, checkMetric("request_rate", latest.RequestsPerSec, reqVals)...)
	anomalies = append(anomalies, checkMetric("latency_p99", latest.LatencyP99Ms, latVals)...)
	anomalies = append(anomalies, checkMetric("error_rate", latest.ErrorRatePct, errVals)...)

	if len(anomalies) == 0 {
		return nil, nil // no anomalies
	}

	// Find the worst anomaly
	worst := anomalies[0]
	for _, a := range anomalies[1:] {
		if math.Abs(a.zScore) > math.Abs(worst.zScore) {
			worst = a
		}
	}

	var affectedMetrics []string
	for _, a := range anomalies {
		affectedMetrics = append(affectedMetrics, a.metric)
	}

	severity := domain.AlertSeverityWarning
	if math.Abs(worst.zScore) > 4 || len(anomalies) >= 3 {
		severity = domain.AlertSeverityCritical
	}

	anomalyType := domain.AnomalyTypeSpike
	if worst.zScore < 0 {
		anomalyType = domain.AnomalyTypeDrop
	}
	if len(anomalies) >= 2 {
		anomalyType = domain.AnomalyTypeCorrelationBreak
	}

	confidence := math.Min(1.0, math.Abs(worst.zScore)/5.0)

	score := &domain.AnomalyScore{
		WorkloadID:      workloadID,
		Score:           confidence,
		AnomalyType:     anomalyType,
		Severity:        severity,
		AffectedMetrics: affectedMetrics,
		ZScore:          worst.zScore,
		Confidence:      confidence,
		Description:     worst.description,
		SuggestedAction: suggestAction(anomalyType, affectedMetrics),
		DetectedAt:      time.Now(),
	}

	return score, nil
}

// DetectAndAlert runs detection and creates alerts if needed
func (d *AnomalyDetector) DetectAndAlert(ctx context.Context, workloadID uuid.UUID) error {
	score, err := d.Detect(ctx, workloadID)
	if err != nil {
		return err
	}
	if score == nil {
		return nil
	}

	if score.Confidence < 0.7 {
		return nil // not confident enough
	}

	w, err := d.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	// Create alert
	alert := &domain.AugurAlert{
		ID:             uuid.New(),
		OrganizationID: w.OrganizationID,
		WorkloadID:     workloadID,
		WorkloadName:   w.Name,
		FeatureID:      w.FeatureID,
		Type:           domain.AlertTypeAnomaly,
		Severity:       score.Severity,
		Title:          fmt.Sprintf("Anomaly detected: %s on %s", score.AnomalyType, w.Name),
		Description:    score.Description,
		FiredAt:        time.Now(),
	}

	if err := d.alertRepo.Create(ctx, alert); err != nil {
		logger.Error("Failed to create anomaly alert: %v", err)
	}

	// Publish event
	featureID := ""
	if w.FeatureID != nil {
		featureID = w.FeatureID.String()
	}

	_ = d.publisher.Publish(ctx, events.EventAnomalyDetected, events.EventData{
		ResourceID:   workloadID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"feature_id":        featureID,
			"anomaly_type":      string(score.AnomalyType),
			"severity":          string(score.Severity),
			"affected_metrics":  score.AffectedMetrics,
			"z_score":           score.ZScore,
			"confidence":        score.Confidence,
			"description":       score.Description,
			"suggested_action":  score.SuggestedAction,
			"deploy_correlated": score.DeployCorrelated,
		},
	})

	return nil
}

type metricAnomaly struct {
	metric      string
	zScore      float64
	description string
}

func checkMetric(name string, current float64, history []float64) []metricAnomaly {
	mean, stddev := meanStddev(history)
	if stddev == 0 {
		return nil
	}

	z := (current - mean) / stddev
	if math.Abs(z) < 3 {
		return nil
	}

	direction := "above"
	if z < 0 {
		direction = "below"
	}

	return []metricAnomaly{{
		metric:      name,
		zScore:      z,
		description: fmt.Sprintf("%s is %.1fσ %s mean (current: %.2f, mean: %.2f)", name, math.Abs(z), direction, current, mean),
	}}
}

func meanStddev(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}

	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))

	var sqSum float64
	for _, v := range vals {
		sqSum += (v - mean) * (v - mean)
	}
	stddev := math.Sqrt(sqSum / float64(len(vals)))

	return mean, stddev
}

func suggestAction(anomalyType domain.AnomalyType, metrics []string) string {
	switch anomalyType {
	case domain.AnomalyTypeSpike:
		return "Scale up to handle increased load"
	case domain.AnomalyTypeDrop:
		return "Investigate upstream routing — request rate dropped unexpectedly"
	case domain.AnomalyTypeCorrelationBreak:
		return "Check for memory leak or background job contention — multiple metrics diverging"
	default:
		return "Investigate workload health"
	}
}
