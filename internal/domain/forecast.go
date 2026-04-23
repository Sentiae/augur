package domain

import (
	"time"

	"github.com/google/uuid"
)

// ForecastModel represents which ML model was used
type ForecastModel string

const (
	ForecastModelHoltWinters ForecastModel = "holt_winters"
	ForecastModelFFT         ForecastModel = "fft"
	ForecastModelNHiTS       ForecastModel = "n_hits"
	ForecastModelTFT         ForecastModel = "tft"
	ForecastModelChronos     ForecastModel = "chronos"
)

// Forecast represents an ML-generated demand forecast for a workload
type Forecast struct {
	WorkloadID             uuid.UUID       `json:"workload_id"`
	GeneratedAt            time.Time       `json:"generated_at"`
	HorizonHours           int             `json:"horizon_hours"`
	ModelUsed              ForecastModel   `json:"model_used"`
	Confidence             float64         `json:"confidence"`
	Points                 []ForecastPoint `json:"points"`
	PredictedPeakAt        *time.Time      `json:"predicted_peak_at,omitempty"`
	PredictedPeakPct       float64         `json:"predicted_peak_pct"` // % above current baseline
	RecommendedMaxReplicas int             `json:"recommended_max_replicas"`
	PreScaleRecommendedAt  *time.Time      `json:"pre_scale_recommended_at,omitempty"`
}

// ForecastPoint represents a single point in a forecast time series
type ForecastPoint struct {
	Timestamp time.Time `json:"timestamp"`
	P10       float64   `json:"p10"`
	P50       float64   `json:"p50"`
	P90       float64   `json:"p90"`
}

// ModelRegistry tracks ONNX model versions
type ModelRegistry struct {
	ID           uuid.UUID     `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	ModelName    string        `json:"model_name" gorm:"not null"`
	ModelVersion string        `json:"model_version" gorm:"not null"`
	ModelType    ForecastModel `json:"model_type" gorm:"type:varchar(20);not null"`
	FilePath     string        `json:"file_path" gorm:"not null"`
	AccuracyMAE  float64       `json:"accuracy_mae"`
	AccuracyMAPE float64       `json:"accuracy_mape"`
	TrainedAt    time.Time     `json:"trained_at"`
	Active       bool          `json:"active" gorm:"default:false"`
	CreatedAt    time.Time     `json:"created_at" gorm:"autoCreateTime"`
}

func (ModelRegistry) TableName() string {
	return "augur_model_registry"
}
