package ml

import (
	"fmt"
	"math"
)

// HoltWinters implements triple exponential smoothing (Holt-Winters additive method)
// for time series forecasting with trend and seasonality components.
//
// The model decomposes a time series into:
//   - Level (L): the baseline value
//   - Trend (T): the rate of change
//   - Seasonal (S): repeating patterns of length `seasonLen`
//
// Parameters:
//   - alpha: smoothing factor for level (0-1)
//   - beta:  smoothing factor for trend (0-1)
//   - gamma: smoothing factor for seasonality (0-1)
type HoltWinters struct {
	Alpha     float64 // level smoothing (0.1-0.3 typical)
	Beta      float64 // trend smoothing (0.01-0.1 typical)
	Gamma     float64 // seasonal smoothing (0.1-0.3 typical)
	SeasonLen int     // season length in data points (e.g., 24 for hourly data with daily cycle)

	// Fitted state
	level    float64
	trend    float64
	seasonal []float64
	fitted   bool
}

// NewHoltWinters creates a new Holt-Winters model with sensible defaults.
// seasonLen is the number of data points in one seasonal cycle.
func NewHoltWinters(seasonLen int) *HoltWinters {
	return &HoltWinters{
		Alpha:     0.2,
		Beta:      0.05,
		Gamma:     0.15,
		SeasonLen: seasonLen,
	}
}

// Fit trains the model on historical data.
// data must have at least 2 * seasonLen points for meaningful results.
func (hw *HoltWinters) Fit(data []float64) error {
	n := len(data)
	if n < 2*hw.SeasonLen {
		return fmt.Errorf("need at least %d data points (2 seasons), got %d", 2*hw.SeasonLen, n)
	}

	sl := hw.SeasonLen

	// Initialize level: mean of first season
	hw.level = 0
	for i := 0; i < sl; i++ {
		hw.level += data[i]
	}
	hw.level /= float64(sl)

	// Initialize trend: average change between first two seasons
	hw.trend = 0
	for i := 0; i < sl; i++ {
		hw.trend += (data[sl+i] - data[i])
	}
	hw.trend /= float64(sl * sl)

	// Initialize seasonal components: deviation from initial level
	hw.seasonal = make([]float64, sl)
	for i := 0; i < sl; i++ {
		hw.seasonal[i] = data[i] - hw.level
	}

	// Fit through all data points using the update equations
	for t := sl; t < n; t++ {
		prevLevel := hw.level
		seasonIdx := t % sl

		// Update level: L_t = alpha * (Y_t - S_{t-m}) + (1-alpha) * (L_{t-1} + T_{t-1})
		hw.level = hw.Alpha*(data[t]-hw.seasonal[seasonIdx]) +
			(1-hw.Alpha)*(prevLevel+hw.trend)

		// Update trend: T_t = beta * (L_t - L_{t-1}) + (1-beta) * T_{t-1}
		hw.trend = hw.Beta*(hw.level-prevLevel) + (1-hw.Beta)*hw.trend

		// Update seasonal: S_t = gamma * (Y_t - L_t) + (1-gamma) * S_{t-m}
		hw.seasonal[seasonIdx] = hw.Gamma*(data[t]-hw.level) + (1-hw.Gamma)*hw.seasonal[seasonIdx]
	}

	hw.fitted = true
	return nil
}

// Forecast produces h steps ahead predictions.
// Returns point forecasts. Use ForecastWithIntervals for confidence intervals.
func (hw *HoltWinters) Forecast(h int) ([]float64, error) {
	if !hw.fitted {
		return nil, fmt.Errorf("model not fitted — call Fit() first")
	}

	forecasts := make([]float64, h)
	for i := 0; i < h; i++ {
		seasonIdx := i % hw.SeasonLen
		forecasts[i] = hw.level + float64(i+1)*hw.trend + hw.seasonal[seasonIdx]
		if forecasts[i] < 0 {
			forecasts[i] = 0 // non-negative constraint
		}
	}
	return forecasts, nil
}

// ForecastWithIntervals produces h steps ahead predictions with P10/P50/P90 intervals.
// The width of intervals grows with the forecast horizon (uncertainty increases).
func (hw *HoltWinters) ForecastWithIntervals(h int, residualStddev float64) (p10, p50, p90 []float64, err error) {
	point, err := hw.Forecast(h)
	if err != nil {
		return nil, nil, nil, err
	}

	p10 = make([]float64, h)
	p50 = make([]float64, h)
	p90 = make([]float64, h)

	for i := 0; i < h; i++ {
		// Uncertainty grows with sqrt(horizon)
		uncertainty := residualStddev * math.Sqrt(float64(i+1))

		p50[i] = point[i]
		p10[i] = math.Max(0, point[i]-1.28*uncertainty) // 10th percentile (z=1.28)
		p90[i] = point[i] + 1.28*uncertainty             // 90th percentile
	}

	return p10, p50, p90, nil
}

// ComputeResidualStddev computes the standard deviation of prediction residuals.
// Used for confidence interval estimation.
func (hw *HoltWinters) ComputeResidualStddev(data []float64) float64 {
	if len(data) < 2*hw.SeasonLen {
		return 0
	}

	// Re-fit and compute one-step-ahead residuals
	sl := hw.SeasonLen
	level := hw.level
	trend := hw.trend
	seasonal := make([]float64, len(hw.seasonal))
	copy(seasonal, hw.seasonal)

	// Re-initialize for residual computation
	level = 0
	for i := 0; i < sl; i++ {
		level += data[i]
	}
	level /= float64(sl)

	trend = 0
	for i := 0; i < sl; i++ {
		trend += (data[sl+i] - data[i])
	}
	trend /= float64(sl * sl)

	for i := 0; i < sl; i++ {
		seasonal[i] = data[i] - level
	}

	var sumSqResiduals float64
	count := 0

	for t := sl; t < len(data); t++ {
		seasonIdx := t % sl
		predicted := level + trend + seasonal[seasonIdx]
		residual := data[t] - predicted
		sumSqResiduals += residual * residual
		count++

		prevLevel := level
		level = hw.Alpha*(data[t]-seasonal[seasonIdx]) + (1-hw.Alpha)*(prevLevel+trend)
		trend = hw.Beta*(level-prevLevel) + (1-hw.Beta)*trend
		seasonal[seasonIdx] = hw.Gamma*(data[t]-level) + (1-hw.Gamma)*seasonal[seasonIdx]
	}

	if count == 0 {
		return 0
	}
	return math.Sqrt(sumSqResiduals / float64(count))
}

// GetLevel returns the current fitted level
func (hw *HoltWinters) GetLevel() float64 { return hw.level }

// GetTrend returns the current fitted trend
func (hw *HoltWinters) GetTrend() float64 { return hw.trend }

// GetSeasonal returns the fitted seasonal components
func (hw *HoltWinters) GetSeasonal() []float64 {
	s := make([]float64, len(hw.seasonal))
	copy(s, hw.seasonal)
	return s
}
