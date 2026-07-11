package domain

import (
	"time"

	"github.com/google/uuid"
)

// WorkloadType represents the type of infrastructure a workload runs on
type WorkloadType string

const (
	WorkloadTypeFirecracker WorkloadType = "firecracker"
	WorkloadTypeKubernetes  WorkloadType = "kubernetes"
	WorkloadTypeVM          WorkloadType = "vm"
	WorkloadTypeServerless  WorkloadType = "serverless"
)

// WorkloadStatus represents the current operational status of a workload
type WorkloadStatus string

const (
	WorkloadStatusHealthy      WorkloadStatus = "healthy"
	WorkloadStatusDegraded     WorkloadStatus = "degraded"
	WorkloadStatusScaling      WorkloadStatus = "scaling"
	WorkloadStatusPaused       WorkloadStatus = "paused"
	WorkloadStatusCircuitOpen  WorkloadStatus = "circuit_open"
	WorkloadStatusObserving    WorkloadStatus = "observing"
)

// OptimizationMode represents the scaling optimization priority
type OptimizationMode string

const (
	OptimizationModeCost        OptimizationMode = "cost"
	OptimizationModePerformance OptimizationMode = "performance"
	OptimizationModeBalanced    OptimizationMode = "balanced"
	OptimizationModeReliability OptimizationMode = "reliability"
)

// Workload represents a managed infrastructure workload
type Workload struct {
	ID                 uuid.UUID        `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OrganizationID     uuid.UUID        `json:"organization_id" gorm:"type:uuid;not null;index"`
	Name               string           `json:"name" gorm:"not null"`
	WorkloadType       WorkloadType     `json:"workload_type" gorm:"type:varchar(20);not null"`
	Environment        string           `json:"environment" gorm:"not null"`
	GroupName          string           `json:"group_name,omitempty" gorm:"column:group_name"`
	FeatureID          *uuid.UUID       `json:"feature_id,omitempty" gorm:"type:uuid;index"`
	SpecID             *uuid.UUID       `json:"spec_id,omitempty" gorm:"type:uuid"`
	ExternalRef        string           `json:"external_ref,omitempty" gorm:"column:external_ref"` // k8s deployment name, firecracker vm id, etc.
	CurrentReplicas    int              `json:"current_replicas" gorm:"default:1"`
	DesiredReplicas    int              `json:"desired_replicas" gorm:"default:1"`
	MinReplicas        int              `json:"min_replicas" gorm:"default:1"`
	MaxReplicas        int              `json:"max_replicas" gorm:"default:10"`
	OptimizationMode   OptimizationMode `json:"optimization_mode" gorm:"type:varchar(20);default:'balanced'"`
	Status             WorkloadStatus   `json:"status" gorm:"type:varchar(20);default:'observing'"`
	ObserveMode        bool             `json:"observe_mode" gorm:"default:true"`
	ObserveUntil       *time.Time       `json:"observe_until,omitempty"`
	AutoscalingEnabled bool             `json:"autoscaling_enabled" gorm:"default:true"`
	AutoscalingPaused  bool             `json:"autoscaling_paused" gorm:"default:false"`
	PausedUntil        *time.Time       `json:"paused_until,omitempty"`
	PauseReason        string           `json:"pause_reason,omitempty"`

	// Cost tracking
	MonthlyCostUSD    float64  `json:"monthly_cost_usd" gorm:"default:0"`
	MonthlyBudgetUSD  *float64 `json:"monthly_budget_usd,omitempty"`
	HourlyCostUSD     float64  `json:"hourly_cost_usd" gorm:"default:0"`

	// Current metrics snapshot
	CPUUtilizationPct    float64 `json:"cpu_utilization_pct" gorm:"default:0"`
	MemoryUtilizationPct float64 `json:"memory_utilization_pct" gorm:"default:0"`
	RequestsPerSec       float64 `json:"requests_per_sec" gorm:"default:0"`
	LatencyP99Ms         float64 `json:"latency_p99_ms" gorm:"default:0"`
	ErrorRatePct         float64 `json:"error_rate_pct" gorm:"default:0"`

	// SLO tracking
	SLOCompliancePct float64 `json:"slo_compliance_pct" gorm:"default:100"`

	// Circuit breaker
	ConsecutiveFailures int  `json:"consecutive_failures" gorm:"default:0"`
	CircuitBreakerOpen  bool `json:"circuit_breaker_open" gorm:"default:false"`

	// Timestamps
	LastScaledAt    *time.Time `json:"last_scaled_at,omitempty"`
	LastMetricsAt   *time.Time `json:"last_metrics_at,omitempty"`
	LastDecisionAt  *time.Time `json:"last_decision_at,omitempty"`
	LastDecisionReasoning string `json:"last_decision_reasoning,omitempty" gorm:"type:text"`
	CreatedAt       time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (Workload) TableName() string {
	return "augur_workloads"
}

// WorkloadMetricsSnapshot represents a point-in-time metrics reading
type WorkloadMetricsSnapshot struct {
	ID             uuid.UUID `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	WorkloadID     uuid.UUID `json:"workload_id" gorm:"type:uuid;not null;index:idx_metrics_workload_ts"`
	OrganizationID uuid.UUID `json:"organization_id" gorm:"type:uuid;index:idx_augur_metrics_org"`
	Timestamp      time.Time `json:"timestamp" gorm:"not null;index:idx_metrics_workload_ts"`
	CPUPct         float64   `json:"cpu_pct"`
	MemoryPct      float64   `json:"memory_pct"`
	RequestsPerSec float64   `json:"requests_per_sec"`
	LatencyP99Ms   float64   `json:"latency_p99_ms"`
	ErrorRatePct   float64   `json:"error_rate_pct"`
	Replicas       int       `json:"replicas"`
	CostUSDPerHour float64   `json:"cost_usd_per_hour"`
}

func (WorkloadMetricsSnapshot) TableName() string {
	return "augur_workload_metrics"
}
