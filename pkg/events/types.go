package events

import (
	"time"

	kafka "github.com/sentiae/platform-kit/kafka"
)

// EventData is the platform-kit event data type re-exported for convenience.
type EventData = kafka.EventData

// Event type constants for augur events.
// These are the event type strings without the "sentiae." prefix because
// the platform-kit publisher prepends the TopicPrefix when deriving topics.
const (
	SourceName = "infrastructure-intelligence-service"
	Topic      = "sentiae.augur.events"

	EventWorkloadRegistered     = "augur.workload.registered"
	EventWorkloadObserved       = "augur.workload.observed"
	EventScaleTriggered         = "augur.scale.triggered"
	EventScaleCompleted         = "augur.scale.completed"
	EventScaleFailed            = "augur.scale.failed"
	EventScaleRolledBack        = "augur.scale.rolled_back"
	EventAnomalyDetected        = "augur.anomaly.detected"
	EventAnomalyResolved        = "augur.anomaly.resolved"
	EventSpikePredicted         = "augur.forecast.spike_predicted"
	EventCostThresholdExceeded  = "augur.cost.threshold_exceeded"
	EventIdleDetected           = "augur.cost.idle_detected"
	EventOptimizationFound      = "augur.cost.optimization_found"
	EventSLOBreachDetected      = "augur.slo.breach_detected"
	EventSLOBudgetWarning       = "augur.slo.budget_warning"
	EventSLOBudgetExhausted     = "augur.slo.budget_exhausted"
	EventPolicyViolation        = "augur.policy.violation"
	EventSpotInterruptPredicted = "augur.spot.interruption_predicted"
	EventSpotInterrupted        = "augur.spot.interrupted"
	EventCircuitBreakerOpened   = "augur.circuit_breaker.opened"
	EventFeatureHealthDegraded  = "augur.feature.health_degraded"
)

// ScaleTriggeredMetadata is the event payload for scaling actions
type ScaleTriggeredMetadata struct {
	WorkloadID       string  `json:"workload_id"`
	WorkloadName     string  `json:"workload_name"`
	FeatureID        string  `json:"feature_id,omitempty"`
	SpecID           string  `json:"spec_id,omitempty"`
	WorkloadType     string  `json:"workload_type"`
	CurrentReplicas  int     `json:"current_replicas"`
	TargetReplicas   int     `json:"target_replicas"`
	ScaleDirection   string  `json:"scale_direction"`
	Trigger          string  `json:"trigger"`
	OptimizationMode string  `json:"optimization_mode"`
	EstCostDeltaUSD  float64 `json:"est_cost_delta_usd"`
	Reasoning        string  `json:"reasoning"`
	Confidence       float64 `json:"confidence"`
}

// AnomalyDetectedMetadata is the event payload for anomaly detection
type AnomalyDetectedMetadata struct {
	WorkloadID       string   `json:"workload_id"`
	FeatureID        string   `json:"feature_id,omitempty"`
	AnomalyType      string   `json:"anomaly_type"`
	Severity         string   `json:"severity"`
	AffectedMetrics  []string `json:"affected_metrics"`
	ZScore           float64  `json:"z_score"`
	Confidence       float64  `json:"confidence"`
	Description      string   `json:"description"`
	SuggestedAction  string   `json:"suggested_action"`
	DeployCorrelated bool     `json:"deploy_correlated"`
}

// SpikePredictedMetadata is the event payload for demand spike prediction
type SpikePredictedMetadata struct {
	WorkloadID        string    `json:"workload_id"`
	FeatureID         string    `json:"feature_id,omitempty"`
	PredictedPeakAt   time.Time `json:"predicted_peak_at"`
	PredictedPeakPct  float64   `json:"predicted_peak_pct"`
	HorizonMinutes    int       `json:"horizon_minutes"`
	Confidence        float64   `json:"confidence"`
	ModelUsed         string    `json:"model_used"`
	PreScaleInitiated bool      `json:"pre_scale_initiated"`
	PreScaleAt        time.Time `json:"pre_scale_at"`
}

// SLOBreachMetadata is the event payload for SLO breaches
type SLOBreachMetadata struct {
	WorkloadID       string  `json:"workload_id"`
	FeatureID        string  `json:"feature_id,omitempty"`
	SLOType          string  `json:"slo_type"`
	SLOTarget        float64 `json:"slo_target"`
	CurrentValue     float64 `json:"current_value"`
	BurnRate         float64 `json:"burn_rate"`
	Window           string  `json:"window"`
	BudgetPctLeft    float64 `json:"budget_pct_left"`
	ActionTaken      string  `json:"action_taken"`
	DeployCorrelated bool    `json:"deploy_correlated"`
}

// CostThresholdMetadata is the event payload for cost threshold alerts
type CostThresholdMetadata struct {
	Scope        string  `json:"scope"`
	ScopeID      string  `json:"scope_id"`
	BudgetUSD    float64 `json:"budget_usd"`
	ActualUSD    float64 `json:"actual_usd"`
	ProjectedUSD float64 `json:"projected_usd"`
	ThresholdPct int     `json:"threshold_pct"`
}

// CircuitBreakerMetadata is the event payload for circuit breaker events
type CircuitBreakerMetadata struct {
	WorkloadID          string `json:"workload_id"`
	WorkloadName        string `json:"workload_name"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastFailureReason   string `json:"last_failure_reason"`
}
