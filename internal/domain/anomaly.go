package domain

import (
	"time"

	"github.com/google/uuid"
)

// AnomalyType represents the type of anomaly detected
type AnomalyType string

const (
	AnomalyTypeSpike            AnomalyType = "spike"
	AnomalyTypeDrop             AnomalyType = "drop"
	AnomalyTypePatternBreak     AnomalyType = "pattern_break"
	AnomalyTypeCorrelationBreak AnomalyType = "correlation_break"
)

// AlertSeverity represents the severity of an alert
type AlertSeverity string

const (
	AlertSeverityWarning  AlertSeverity = "warning"
	AlertSeverityCritical AlertSeverity = "critical"
)

// AlertType represents the type of an alert
type AlertType string

const (
	AlertTypeSLOBreach        AlertType = "slo_breach"
	AlertTypeAnomaly          AlertType = "anomaly"
	AlertTypeCostThreshold    AlertType = "cost_threshold"
	AlertTypeSpotInterruption AlertType = "spot_interruption"
	AlertTypeCircuitBreaker   AlertType = "circuit_breaker"
)

// AugurAlert represents an active or resolved alert
type AugurAlert struct {
	ID              uuid.UUID     `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OrganizationID  uuid.UUID     `json:"organization_id" gorm:"type:uuid;not null;index"`
	WorkloadID      uuid.UUID     `json:"workload_id" gorm:"type:uuid;not null;index"`
	WorkloadName    string        `json:"workload_name" gorm:"not null"`
	FeatureID       *uuid.UUID    `json:"feature_id,omitempty" gorm:"type:uuid"`
	Type            AlertType     `json:"type" gorm:"type:varchar(30);not null"`
	Severity        AlertSeverity `json:"severity" gorm:"type:varchar(20);not null"`
	Title           string        `json:"title" gorm:"not null"`
	Description     string        `json:"description" gorm:"type:text"`
	AutoActionTaken string        `json:"auto_action_taken,omitempty" gorm:"type:text"`
	FiredAt         time.Time     `json:"fired_at" gorm:"not null"`
	ResolvedAt      *time.Time    `json:"resolved_at,omitempty"`
	CreatedAt       time.Time     `json:"created_at" gorm:"autoCreateTime"`
}

func (AugurAlert) TableName() string {
	return "augur_alerts"
}

// AnomalyScore represents the output of the anomaly detection pipeline
type AnomalyScore struct {
	WorkloadID      uuid.UUID   `json:"workload_id"`
	Score           float64     `json:"score"` // 0.0 - 1.0
	AnomalyType     AnomalyType `json:"anomaly_type"`
	Severity        AlertSeverity `json:"severity"`
	AffectedMetrics []string    `json:"affected_metrics"`
	ZScore          float64     `json:"z_score"`
	Confidence      float64     `json:"confidence"`
	Description     string      `json:"description"`
	SuggestedAction string      `json:"suggested_action"`
	DeployCorrelated bool       `json:"deploy_correlated"`
	DetectedAt      time.Time   `json:"detected_at"`
}
