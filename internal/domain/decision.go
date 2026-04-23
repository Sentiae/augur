package domain

import (
	"time"

	"github.com/google/uuid"
)

// ScaleTrigger represents what triggered a scaling decision
type ScaleTrigger string

const (
	ScaleTriggerReactive   ScaleTrigger = "reactive"
	ScaleTriggerPredictive ScaleTrigger = "predictive"
	ScaleTriggerSLO        ScaleTrigger = "slo"
	ScaleTriggerManual     ScaleTrigger = "manual"
	ScaleTriggerCost       ScaleTrigger = "cost"
)

// ScaleDirection represents the direction of a scaling action
type ScaleDirection string

const (
	ScaleDirectionUp   ScaleDirection = "up"
	ScaleDirectionDown ScaleDirection = "down"
	ScaleDirectionNone ScaleDirection = "none"
)

// DecisionOutcome represents what happened after a scaling action
type DecisionOutcome string

const (
	DecisionOutcomePending    DecisionOutcome = "pending"
	DecisionOutcomeHealthy    DecisionOutcome = "healthy"
	DecisionOutcomeDegraded   DecisionOutcome = "degraded"
	DecisionOutcomeRolledBack DecisionOutcome = "rolled_back"
	DecisionOutcomeFailed     DecisionOutcome = "failed"
)

// ScalingDecision represents a single scaling decision made by the Decision Engine
type ScalingDecision struct {
	ID               uuid.UUID       `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	WorkloadID       uuid.UUID       `json:"workload_id" gorm:"type:uuid;not null;index"`
	OrganizationID   uuid.UUID       `json:"organization_id" gorm:"type:uuid;not null;index"`
	Trigger          ScaleTrigger    `json:"trigger" gorm:"type:varchar(20);not null"`
	Direction        ScaleDirection  `json:"direction" gorm:"type:varchar(10);not null"`
	FromReplicas     int             `json:"from_replicas" gorm:"not null"`
	ToReplicas       int             `json:"to_replicas" gorm:"not null"`
	Reasoning        string          `json:"reasoning" gorm:"type:text;not null"`
	Confidence       float64         `json:"confidence" gorm:"not null"`
	OptimizationMode OptimizationMode `json:"optimization_mode" gorm:"type:varchar(20)"`

	// Metrics at decision time
	CPUAtDecision        float64 `json:"cpu_at_decision"`
	MemoryAtDecision     float64 `json:"memory_at_decision"`
	RequestRateAtDecision float64 `json:"request_rate_at_decision"`
	LatencyP99AtDecision float64 `json:"latency_p99_at_decision"`
	ErrorRateAtDecision  float64 `json:"error_rate_at_decision"`

	// Policy context
	PolicyApplied    string  `json:"policy_applied" gorm:"type:text"`
	PredictionUsed   bool    `json:"prediction_used"`
	ForecastValue    float64 `json:"forecast_value,omitempty"`

	// Cost impact
	EstCostDeltaUSD float64 `json:"est_cost_delta_usd"`

	// Safety
	DryRun           bool   `json:"dry_run" gorm:"default:false"`
	RequiresApproval bool   `json:"requires_approval" gorm:"default:false"`
	ApprovedBy       string `json:"approved_by,omitempty"`

	// Outcome (observed 5 min post-action)
	Outcome         DecisionOutcome `json:"outcome" gorm:"type:varchar(20);default:'pending'"`
	OutcomeObserved *time.Time      `json:"outcome_observed,omitempty"`

	// Rollback reference
	RollbackOfID *uuid.UUID `json:"rollback_of_id,omitempty" gorm:"type:uuid"`

	// Timestamps
	DecidedAt  time.Time `json:"decided_at" gorm:"autoCreateTime"`
	ExecutedAt *time.Time `json:"executed_at,omitempty"`
}

func (ScalingDecision) TableName() string {
	return "augur_scaling_decisions"
}

// ScalingCandidate represents a candidate action scored by the Decision Engine
type ScalingCandidate struct {
	TargetReplicas  int
	CostDeltaUSD    float64
	LatencyImpact   float64
	ReliabilityGain float64
}

// PolicyWeights represents the scoring weights derived from policy
type PolicyWeights struct {
	Cost        float64
	Performance float64
	Reliability float64
}

// Score computes the multi-objective score for a candidate action
func (w PolicyWeights) Score(c ScalingCandidate) float64 {
	return w.Cost*(-c.CostDeltaUSD) +
		w.Performance*(-c.LatencyImpact) +
		w.Reliability*c.ReliabilityGain
}

// WeightsForMode returns the default weights for a given optimization mode
func WeightsForMode(mode OptimizationMode) PolicyWeights {
	switch mode {
	case OptimizationModeCost:
		return PolicyWeights{Cost: 0.7, Performance: 0.2, Reliability: 0.1}
	case OptimizationModePerformance:
		return PolicyWeights{Cost: 0.1, Performance: 0.7, Reliability: 0.2}
	case OptimizationModeReliability:
		return PolicyWeights{Cost: 0.1, Performance: 0.1, Reliability: 0.8}
	default: // balanced
		return PolicyWeights{Cost: 0.34, Performance: 0.33, Reliability: 0.33}
	}
}
