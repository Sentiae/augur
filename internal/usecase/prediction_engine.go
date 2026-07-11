package usecase

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/ml"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// PredictionEngine runs ML forecasting for workloads every 15 minutes.
// Phase 1: Holt-Winters + FFT in pure Go (no Python dependency).
type PredictionEngine struct {
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
	publisher    events.EventPublisher
	orgLister    OrgLister
	fftAnalyzer  *ml.FFTAnalyzer

	// Cache of latest forecasts per workload
	forecasts map[uuid.UUID]*domain.Forecast
}

func NewPredictionEngine(
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
	publisher events.EventPublisher,
	orgLister OrgLister,
) *PredictionEngine {
	return &PredictionEngine{
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
		publisher:    publisher,
		orgLister:    orgLister,
		fftAnalyzer:  ml.NewFFTAnalyzer(),
		forecasts:    make(map[uuid.UUID]*domain.Forecast),
	}
}

// Run starts the prediction engine loop (every 15 minutes)
func (e *PredictionEngine) Run(ctx context.Context) {
	logger.Info("Prediction engine started (interval=15m)")
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Prediction engine stopped")
			return
		case <-ticker.C:
			e.generateForecasts(ctx)
		}
	}
}

func (e *PredictionEngine) generateForecasts(ctx context.Context) {
	_ = ForEachOrg(ctx, e.orgLister, func(orgCtx context.Context) error {
		workloads, err := e.workloadRepo.FindAllManaged(orgCtx)
		if err != nil {
			logger.Error("Prediction engine: failed to fetch workloads: %v", err)
			return err
		}

		for _, w := range workloads {
			forecast, err := e.forecastWorkload(orgCtx, w.ID, 6) // 6-hour horizon default
			if err != nil {
				logger.Debug("Prediction engine: skipping %s: %v", w.Name, err)
				continue
			}

			e.forecasts[w.ID] = forecast

			// Check if we should publish a spike prediction event
			if forecast.PredictedPeakPct > 100 && forecast.Confidence > 0.7 {
				e.publishSpikePrediction(orgCtx, w, forecast)
			}
		}
		return nil
	})
}

// GetForecast returns the latest forecast for a workload, or generates one on demand
func (e *PredictionEngine) GetForecast(ctx context.Context, workloadID uuid.UUID, horizonHours int) (*domain.Forecast, error) {
	// Check cache first
	if cached, ok := e.forecasts[workloadID]; ok {
		if time.Since(cached.GeneratedAt) < 20*time.Minute {
			return cached, nil
		}
	}

	// Generate fresh forecast
	forecast, err := e.forecastWorkload(ctx, workloadID, horizonHours)
	if err != nil {
		return nil, err
	}

	e.forecasts[workloadID] = forecast
	return forecast, nil
}

// forecastWorkload generates a demand forecast for a single workload
func (e *PredictionEngine) forecastWorkload(ctx context.Context, workloadID uuid.UUID, horizonHours int) (*domain.Forecast, error) {
	// Get 7 days of metrics for training
	since := time.Now().Add(-7 * 24 * time.Hour)
	metrics, err := e.metricsRepo.FindByWorkload(ctx, workloadID, since, 10000)
	if err != nil {
		return nil, err
	}

	if len(metrics) < 48 { // need at least 2 days of hourly data
		return nil, fmt.Errorf("insufficient data: need 48+ points, have %d", len(metrics))
	}

	// Extract request rate time series (primary demand signal)
	requestRates := make([]float64, len(metrics))
	for i, m := range metrics {
		requestRates[len(metrics)-1-i] = m.RequestsPerSec // reverse to chronological order
	}

	// Determine sampling rate (samples per hour)
	var samplesPerHour float64
	if len(metrics) >= 2 {
		dt := metrics[0].Timestamp.Sub(metrics[len(metrics)-1].Timestamp)
		samplesPerHour = float64(len(metrics)) / dt.Hours()
	}
	if samplesPerHour <= 0 {
		samplesPerHour = 1
	}

	// Use FFT to detect optimal seasonal period
	seasonLen := e.fftAnalyzer.DetectSeasonLength(requestRates, samplesPerHour)
	if seasonLen < 6 {
		seasonLen = int(samplesPerHour * 24) // default to daily cycle
	}
	if seasonLen < 6 {
		seasonLen = 24 // absolute minimum
	}
	// Ensure we have enough data for the season
	if len(requestRates) < 2*seasonLen {
		seasonLen = len(requestRates) / 3
		if seasonLen < 4 {
			seasonLen = 4
		}
	}

	// Fit Holt-Winters model
	hw := ml.NewHoltWinters(seasonLen)
	if err := hw.Fit(requestRates); err != nil {
		return nil, err
	}

	// Forecast horizon in data points
	forecastPoints := int(float64(horizonHours) * samplesPerHour)
	if forecastPoints < 1 {
		forecastPoints = horizonHours // at least 1 point per hour
	}

	residStd := hw.ComputeResidualStddev(requestRates)
	p10, p50, p90, err := hw.ForecastWithIntervals(forecastPoints, residStd)
	if err != nil {
		return nil, err
	}

	// Build forecast points with timestamps
	now := time.Now()
	interval := time.Duration(float64(time.Hour) / samplesPerHour)
	points := make([]domain.ForecastPoint, forecastPoints)
	for i := range forecastPoints {
		points[i] = domain.ForecastPoint{
			Timestamp: now.Add(time.Duration(i+1) * interval),
			P10:       p10[i],
			P50:       p50[i],
			P90:       p90[i],
		}
	}

	// Find predicted peak
	var peakIdx int
	var peakVal float64
	for i, p := range p50 {
		if p > peakVal {
			peakVal = p
			peakIdx = i
		}
	}

	// Current baseline (last few data points average)
	baseline := 0.0
	recentN := int(math.Min(float64(len(requestRates)), 10))
	for i := len(requestRates) - recentN; i < len(requestRates); i++ {
		baseline += requestRates[i]
	}
	baseline /= float64(recentN)

	peakPct := 0.0
	if baseline > 0 {
		peakPct = ((peakVal - baseline) / baseline) * 100
	}

	// Compute confidence based on model fit quality
	confidence := math.Max(0.3, 1.0-residStd/(baseline+1))
	confidence = math.Min(confidence, 0.95)

	// Recommended max replicas: capacity to handle P90 peak
	w, _ := e.workloadRepo.FindByID(ctx, workloadID)
	recommendedMax := 1
	if w != nil && w.CurrentReplicas > 0 && baseline > 0 {
		scaleFactor := p90[peakIdx] / baseline
		recommendedMax = int(math.Ceil(float64(w.CurrentReplicas) * scaleFactor))
		if recommendedMax < w.CurrentReplicas {
			recommendedMax = w.CurrentReplicas
		}
	}

	// Pre-scale time: 30 minutes before peak
	peakTime := points[peakIdx].Timestamp
	preScaleAt := peakTime.Add(-30 * time.Minute)

	forecast := &domain.Forecast{
		WorkloadID:             workloadID,
		GeneratedAt:            now,
		HorizonHours:           horizonHours,
		ModelUsed:              domain.ForecastModelHoltWinters,
		Confidence:             confidence,
		Points:                 points,
		PredictedPeakAt:        &peakTime,
		PredictedPeakPct:       peakPct,
		RecommendedMaxReplicas: recommendedMax,
		PreScaleRecommendedAt:  &preScaleAt,
	}

	return forecast, nil
}

func (e *PredictionEngine) publishSpikePrediction(ctx context.Context, w *domain.Workload, f *domain.Forecast) {
	horizonMin := 0
	if f.PredictedPeakAt != nil {
		horizonMin = int(time.Until(*f.PredictedPeakAt).Minutes())
	}

	featureID := ""
	if w.FeatureID != nil {
		featureID = w.FeatureID.String()
	}

	preScaleAt := time.Now()
	if f.PreScaleRecommendedAt != nil {
		preScaleAt = *f.PreScaleRecommendedAt
	}

	_ = e.publisher.Publish(ctx, events.EventSpikePredicted, events.EventData{
		ResourceID:   w.ID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"feature_id":         featureID,
			"predicted_peak_at":  f.PredictedPeakAt,
			"predicted_peak_pct": f.PredictedPeakPct,
			"horizon_minutes":    horizonMin,
			"confidence":         f.Confidence,
			"model_used":         string(f.ModelUsed),
			"pre_scale_initiated": false,
			"pre_scale_at":       preScaleAt,
		},
	})

	logger.Info("Spike predicted: workload=%s, peak=+%.0f%% in %dmin, confidence=%.2f",
		w.Name, f.PredictedPeakPct, horizonMin, f.Confidence)
}

// HasSpikePrediction checks if there's a predicted spike within the given horizon
func (e *PredictionEngine) HasSpikePrediction(workloadID uuid.UUID, withinMinutes int) (*domain.Forecast, bool) {
	forecast, ok := e.forecasts[workloadID]
	if !ok || forecast.PredictedPeakAt == nil {
		return nil, false
	}

	minutesToPeak := time.Until(*forecast.PredictedPeakAt).Minutes()
	if minutesToPeak > 0 && minutesToPeak <= float64(withinMinutes) && forecast.PredictedPeakPct > 50 {
		return forecast, true
	}

	return nil, false
}
