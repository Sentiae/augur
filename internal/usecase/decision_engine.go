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

// DecisionEngine runs the 30-second decision loop per workload
type DecisionEngine struct {
	workloadRepo *postgres.WorkloadRepository
	decisionRepo *postgres.DecisionRepository
	policyRepo   *postgres.PolicyRepository
	alertRepo    *postgres.AlertRepository
	publisher    events.EventPublisher
	celEval      *CELEvaluator
	predictor    *PredictionEngine
	interval     time.Duration
	maxPerHour   int
	cooldownUp   time.Duration
	cooldownDown time.Duration
	cbThreshold  int
	rollbackMin  int
}

func NewDecisionEngine(
	workloadRepo *postgres.WorkloadRepository,
	decisionRepo *postgres.DecisionRepository,
	policyRepo *postgres.PolicyRepository,
	alertRepo *postgres.AlertRepository,
	publisher events.EventPublisher,
	intervalSec int,
	maxPerHour int,
	cooldownUp time.Duration,
	cooldownDown time.Duration,
	cbThreshold int,
	rollbackMin int,
) *DecisionEngine {
	celEval, err := NewCELEvaluator()
	if err != nil {
		logger.Error("Failed to initialize CEL evaluator: %v (CEL rules disabled)", err)
	}

	return &DecisionEngine{
		workloadRepo: workloadRepo,
		decisionRepo: decisionRepo,
		policyRepo:   policyRepo,
		alertRepo:    alertRepo,
		publisher:    publisher,
		celEval:      celEval,
		interval:     time.Duration(intervalSec) * time.Second,
		maxPerHour:   maxPerHour,
		cooldownUp:   cooldownUp,
		cooldownDown: cooldownDown,
		cbThreshold:  cbThreshold,
		rollbackMin:  rollbackMin,
	}
}

// SetPredictionEngine sets the prediction engine for predictive scaling.
// Called after both are constructed to avoid circular dependency.
func (e *DecisionEngine) SetPredictionEngine(p *PredictionEngine) {
	e.predictor = p
}

// Run starts the decision engine loop. Blocks until ctx is cancelled.
func (e *DecisionEngine) Run(ctx context.Context) {
	logger.Info("Decision engine started (interval=%s)", e.interval)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Decision engine stopped")
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

func (e *DecisionEngine) tick(ctx context.Context) {
	workloads, err := e.workloadRepo.FindActive(ctx)
	if err != nil {
		logger.Error("Decision engine: failed to fetch active workloads: %v", err)
		return
	}

	for _, w := range workloads {
		if err := e.evaluateWorkload(ctx, w); err != nil {
			logger.Error("Decision engine: error evaluating workload %s: %v", w.ID, err)
		}
	}
}

func (e *DecisionEngine) evaluateWorkload(ctx context.Context, w *domain.Workload) error {
	// Check circuit breaker
	if w.CircuitBreakerOpen {
		return nil
	}

	// Check cooldown
	if !e.cooldownExpired(w) {
		return nil
	}

	// Check rate limit
	hourAgo := time.Now().Add(-1 * time.Hour)
	count, err := e.decisionRepo.CountRecentByWorkload(ctx, w.ID, hourAgo)
	if err != nil {
		return err
	}
	if count >= int64(e.maxPerHour) {
		return nil
	}

	// Resolve policy
	policy := e.resolvePolicy(ctx, w)

	// Evaluate CEL scaling rules first (if any)
	if celDecision := e.evaluateCELRules(w, policy); celDecision != nil {
		if err := e.decisionRepo.Create(ctx, celDecision); err != nil {
			return fmt.Errorf("failed to log CEL decision: %w", err)
		}
		e.applyDecision(ctx, w, celDecision)
		return nil
	}

	// Check predictive scaling (ML forecast)
	if predDecision := e.evaluatePredictiveScaling(w, policy); predDecision != nil {
		if err := e.decisionRepo.Create(ctx, predDecision); err != nil {
			return fmt.Errorf("failed to log predictive decision: %w", err)
		}
		e.applyDecision(ctx, w, predDecision)
		return nil
	}

	// Evaluate scaling need based on current metrics (threshold-based fallback)
	decision := e.computeDecision(w, policy)
	if decision == nil {
		return nil // no action needed
	}

	// Log decision
	if err := e.decisionRepo.Create(ctx, decision); err != nil {
		return fmt.Errorf("failed to log decision: %w", err)
	}

	e.applyDecision(ctx, w, decision)
	return nil
}

// applyDecision updates workload state and publishes scaling event
func (e *DecisionEngine) applyDecision(ctx context.Context, w *domain.Workload, decision *domain.ScalingDecision) {
	now := time.Now()
	w.DesiredReplicas = decision.ToReplicas
	w.LastDecisionAt = &now
	w.LastDecisionReasoning = decision.Reasoning
	w.Status = domain.WorkloadStatusScaling
	if err := e.workloadRepo.Update(ctx, w); err != nil {
		logger.Error("Failed to update workload %s: %v", w.Name, err)
		return
	}

	featureID := ""
	if w.FeatureID != nil {
		featureID = w.FeatureID.String()
	}
	_ = e.publisher.Publish(ctx, events.EventScaleTriggered, events.EventData{
		ResourceID:   w.ID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"workload_name":    w.Name,
			"feature_id":       featureID,
			"workload_type":    string(w.WorkloadType),
			"current_replicas": decision.FromReplicas,
			"target_replicas":  decision.ToReplicas,
			"scale_direction":  string(decision.Direction),
			"trigger":          string(decision.Trigger),
			"optimization_mode": string(w.OptimizationMode),
			"est_cost_delta_usd": decision.EstCostDeltaUSD,
			"reasoning":        decision.Reasoning,
			"confidence":       decision.Confidence,
		},
	})

	logger.Info("Scaling decision: workload=%s, %d→%d replicas, trigger=%s, reason=%s",
		w.Name, decision.FromReplicas, decision.ToReplicas, decision.Trigger, decision.Reasoning)
}

// evaluatePredictiveScaling checks ML forecasts and pre-scales if a spike is predicted
func (e *DecisionEngine) evaluatePredictiveScaling(w *domain.Workload, policy domain.ResolvedPolicy) *domain.ScalingDecision {
	if e.predictor == nil {
		return nil
	}

	// Check if there's a spike predicted within the next 30 minutes
	forecast, hasSPike := e.predictor.HasSpikePrediction(w.ID, 30)
	if !hasSPike || forecast == nil {
		return nil
	}

	target := forecast.RecommendedMaxReplicas
	if target <= w.CurrentReplicas {
		return nil
	}

	// Apply policy bounds
	if target > policy.MaxReplicas {
		target = policy.MaxReplicas
	}
	if target == w.CurrentReplicas {
		return nil
	}

	return &domain.ScalingDecision{
		ID:               uuid.New(),
		WorkloadID:       w.ID,
		OrganizationID:   w.OrganizationID,
		Trigger:          domain.ScaleTriggerPredictive,
		Direction:        domain.ScaleDirectionUp,
		FromReplicas:     w.CurrentReplicas,
		ToReplicas:       target,
		Reasoning:        fmt.Sprintf("ML forecast predicts +%.0f%% demand spike in %.0f minutes (model: %s, confidence: %.0f%%)", forecast.PredictedPeakPct, time.Until(*forecast.PredictedPeakAt).Minutes(), forecast.ModelUsed, forecast.Confidence*100),
		Confidence:       forecast.Confidence,
		OptimizationMode: w.OptimizationMode,
		PolicyApplied:    fmt.Sprintf("mode=%s, max=%d", policy.OptimizationMode, policy.MaxReplicas),
		PredictionUsed:   true,
		ForecastValue:    forecast.PredictedPeakPct,
		EstCostDeltaUSD:  float64(target-w.CurrentReplicas) * 0.05,
		Outcome:          domain.DecisionOutcomePending,
	}
}

// evaluateCELRules checks CEL scaling rules and returns a decision if any rule fires
func (e *DecisionEngine) evaluateCELRules(w *domain.Workload, policy domain.ResolvedPolicy) *domain.ScalingDecision {
	if e.celEval == nil || len(policy.ScalingRules) == 0 {
		return nil
	}

	mctx := MetricsContext{
		CPUPct:          w.CPUUtilizationPct,
		MemoryPct:       w.MemoryUtilizationPct,
		RequestRate:     w.RequestsPerSec,
		LatencyP99Ms:    w.LatencyP99Ms,
		ErrorRatePct:    w.ErrorRatePct,
		CurrentReplicas: w.CurrentReplicas,
		MinReplicas:     policy.MinReplicas,
		MaxReplicas:     policy.MaxReplicas,
		OptMode:         string(policy.OptimizationMode),
		CostHourly:      w.HourlyCostUSD,
		CostMonthly:     w.MonthlyCostUSD,
	}

	matched := e.celEval.EvaluateRules(policy.ScalingRules, mctx)
	if len(matched) == 0 {
		return nil
	}

	// Apply the first matching rule
	rule := matched[0]
	target := w.CurrentReplicas

	switch rule.Action {
	case "scale_up":
		if rule.TargetReplicasPct > 0 {
			target = int(math.Ceil(float64(w.CurrentReplicas) * float64(rule.TargetReplicasPct) / 100))
		} else {
			target = w.CurrentReplicas + 1
		}
	case "scale_down":
		if rule.TargetReplicasPct > 0 {
			target = int(math.Ceil(float64(w.CurrentReplicas) * float64(rule.TargetReplicasPct) / 100))
		} else {
			target = w.CurrentReplicas - 1
		}
	case "set_min_replicas":
		if rule.Value > w.CurrentReplicas {
			target = rule.Value
		} else {
			return nil // already above minimum
		}
	default:
		return nil
	}

	// Apply bounds
	if target < policy.MinReplicas {
		target = policy.MinReplicas
	}
	if target > policy.MaxReplicas {
		target = policy.MaxReplicas
	}
	if target == w.CurrentReplicas {
		return nil
	}

	direction := domain.ScaleDirectionUp
	if target < w.CurrentReplicas {
		direction = domain.ScaleDirectionDown
	}

	return &domain.ScalingDecision{
		ID:               uuid.New(),
		WorkloadID:       w.ID,
		OrganizationID:   w.OrganizationID,
		Trigger:          domain.ScaleTriggerReactive,
		Direction:        direction,
		FromReplicas:     w.CurrentReplicas,
		ToReplicas:       target,
		Reasoning:        fmt.Sprintf("CEL rule '%s' fired: %s", rule.Name, rule.Condition),
		Confidence:       0.95,
		OptimizationMode: w.OptimizationMode,
		PolicyApplied:    fmt.Sprintf("CEL rule: %s", rule.Name),
		EstCostDeltaUSD:  float64(target-w.CurrentReplicas) * 0.05,
		RequiresApproval: rule.RequiresApproval,
		Outcome:          domain.DecisionOutcomePending,
	}
}

func (e *DecisionEngine) computeDecision(w *domain.Workload, policy domain.ResolvedPolicy) *domain.ScalingDecision {
	current := w.CurrentReplicas
	target := current

	// Reactive scaling based on thresholds
	var trigger domain.ScaleTrigger
	var reasoning string

	// CPU-based scaling
	if w.CPUUtilizationPct > 80 {
		scaleFactor := math.Ceil(w.CPUUtilizationPct / 60) // target 60% utilization
		target = int(math.Ceil(float64(current) * scaleFactor / (w.CPUUtilizationPct / 100)))
		if target <= current {
			target = current + 1
		}
		trigger = domain.ScaleTriggerReactive
		reasoning = fmt.Sprintf("CPU utilization at %.1f%% exceeds 80%% threshold", w.CPUUtilizationPct)
	}

	// Memory-based scaling (more critical)
	if w.MemoryUtilizationPct > 85 {
		memTarget := current + 1
		if memTarget > target {
			target = memTarget
			trigger = domain.ScaleTriggerReactive
			reasoning = fmt.Sprintf("Memory utilization at %.1f%% exceeds 85%% threshold", w.MemoryUtilizationPct)
		}
	}

	// Latency-based scaling
	if w.LatencyP99Ms > 500 {
		latTarget := int(math.Ceil(float64(current) * 1.5))
		if latTarget > target {
			target = latTarget
			trigger = domain.ScaleTriggerReactive
			reasoning = fmt.Sprintf("Latency P99 at %.0fms exceeds 500ms threshold", w.LatencyP99Ms)
		}
	}

	// Error rate scaling (highest priority)
	if w.ErrorRatePct > 1.0 {
		errTarget := current * 2
		if errTarget > target {
			target = errTarget
			trigger = domain.ScaleTriggerSLO
			reasoning = fmt.Sprintf("Error rate at %.2f%% exceeds 1.0%% threshold — SLO protection", w.ErrorRatePct)
		}
	}

	// Scale-down logic (only if everything is healthy)
	if target == current && w.CPUUtilizationPct < 30 && w.MemoryUtilizationPct < 40 && w.ErrorRatePct < 0.1 && current > policy.MinReplicas {
		target = int(math.Max(float64(policy.MinReplicas), math.Ceil(float64(current)*0.75)))
		if target < current {
			trigger = domain.ScaleTriggerCost
			reasoning = fmt.Sprintf("Resources underutilized (CPU: %.1f%%, Mem: %.1f%%) — scaling down to save cost", w.CPUUtilizationPct, w.MemoryUtilizationPct)
		}
	}

	// No change needed
	if target == current {
		return nil
	}

	// Apply policy bounds
	if target < policy.MinReplicas {
		target = policy.MinReplicas
	}
	if target > policy.MaxReplicas {
		target = policy.MaxReplicas
	}

	// Still no change after bounds
	if target == current {
		return nil
	}

	direction := domain.ScaleDirectionUp
	if target < current {
		direction = domain.ScaleDirectionDown
	}

	// Estimate cost delta (rough: $0.05/hr per replica)
	costDelta := float64(target-current) * 0.05

	return &domain.ScalingDecision{
		ID:               uuid.New(),
		WorkloadID:       w.ID,
		OrganizationID:   w.OrganizationID,
		Trigger:          trigger,
		Direction:        direction,
		FromReplicas:     current,
		ToReplicas:       target,
		Reasoning:        reasoning,
		Confidence:       0.85,
		OptimizationMode: w.OptimizationMode,

		CPUAtDecision:         w.CPUUtilizationPct,
		MemoryAtDecision:      w.MemoryUtilizationPct,
		RequestRateAtDecision: w.RequestsPerSec,
		LatencyP99AtDecision:  w.LatencyP99Ms,
		ErrorRateAtDecision:   w.ErrorRatePct,

		PolicyApplied:  fmt.Sprintf("mode=%s, min=%d, max=%d", policy.OptimizationMode, policy.MinReplicas, policy.MaxReplicas),
		EstCostDeltaUSD: costDelta,

		Outcome: domain.DecisionOutcomePending,
	}
}

func (e *DecisionEngine) cooldownExpired(w *domain.Workload) bool {
	if w.LastScaledAt == nil {
		return true
	}
	// Use the shorter cooldown (scale-up) as default; the longer one (scale-down) is used elsewhere
	return time.Since(*w.LastScaledAt) > e.cooldownUp
}

func (e *DecisionEngine) resolvePolicy(ctx context.Context, w *domain.Workload) domain.ResolvedPolicy {
	global, _ := e.policyRepo.FindGlobal(ctx, w.OrganizationID)
	var group *domain.AugurPolicy
	if w.GroupName != "" {
		group, _ = e.policyRepo.FindByGroup(ctx, w.OrganizationID, w.GroupName)
	}
	app, _ := e.policyRepo.FindByApp(ctx, w.OrganizationID, w.ID.String())

	resolved := domain.ResolvePolicy(global, group, app)

	// Parse CEL scaling rules from all policy levels
	for _, p := range []*domain.AugurPolicy{global, group, app} {
		if p != nil && p.ScalingRules != "" {
			rules, err := ParseScalingRules(p.ScalingRules)
			if err != nil {
				logger.Error("Failed to parse CEL rules for policy %s: %v", p.Name, err)
				continue
			}
			resolved.ScalingRules = append(resolved.ScalingRules, rules...)
		}
	}

	return resolved
}

// RecordOutcome records the outcome of a scaling decision after the observation window
func (e *DecisionEngine) RecordOutcome(ctx context.Context, decisionID uuid.UUID, outcome domain.DecisionOutcome) error {
	d, err := e.decisionRepo.FindByID(ctx, decisionID)
	if err != nil {
		return err
	}

	now := time.Now()
	d.Outcome = outcome
	d.OutcomeObserved = &now

	if err := e.decisionRepo.Update(ctx, d); err != nil {
		return err
	}

	// Handle failures for circuit breaker
	if outcome == domain.DecisionOutcomeFailed || outcome == domain.DecisionOutcomeDegraded {
		w, err := e.workloadRepo.FindByID(ctx, d.WorkloadID)
		if err != nil {
			return err
		}
		w.ConsecutiveFailures++
		if w.ConsecutiveFailures >= e.cbThreshold {
			w.CircuitBreakerOpen = true
			w.Status = domain.WorkloadStatusCircuitOpen
			logger.Warn("Circuit breaker opened for workload %s after %d failures", w.Name, w.ConsecutiveFailures)

			_ = e.publisher.Publish(ctx, events.EventCircuitBreakerOpened, events.EventData{
				ResourceID:   w.ID.String(),
				ResourceType: "workload",
				Metadata: map[string]any{
					"workload_name":        w.Name,
					"consecutive_failures": w.ConsecutiveFailures,
					"last_failure_reason":  d.Reasoning,
				},
			})
		}
		return e.workloadRepo.Update(ctx, w)
	}

	// Reset failure count on success
	if outcome == domain.DecisionOutcomeHealthy {
		w, err := e.workloadRepo.FindByID(ctx, d.WorkloadID)
		if err != nil {
			return err
		}
		w.ConsecutiveFailures = 0
		w.CircuitBreakerOpen = false
		now := time.Now()
		w.LastScaledAt = &now
		w.CurrentReplicas = d.ToReplicas
		w.Status = domain.WorkloadStatusHealthy
		return e.workloadRepo.Update(ctx, w)
	}

	return nil
}

// ExplainDecision returns the full context of a scaling decision
func (e *DecisionEngine) ExplainDecision(ctx context.Context, decisionID uuid.UUID) (*domain.ScalingDecision, error) {
	return e.decisionRepo.FindByID(ctx, decisionID)
}

// GetRecentDecisions returns recent decisions for a workload
func (e *DecisionEngine) GetRecentDecisions(ctx context.Context, workloadID uuid.UUID, limit int) ([]*domain.ScalingDecision, error) {
	return e.decisionRepo.FindByWorkload(ctx, workloadID, limit)
}
