package usecase

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// CELEvaluator evaluates CEL expressions for scaling rules
type CELEvaluator struct {
	env *cel.Env
}

// NewCELEvaluator creates a CEL environment with the standard variables available to scaling rules
func NewCELEvaluator() (*CELEvaluator, error) {
	env, err := cel.NewEnv(
		cel.CrossTypeNumericComparisons(true),

		// Metrics variables
		cel.Variable("metrics.cpu_pct", cel.DoubleType),
		cel.Variable("metrics.memory_pct", cel.DoubleType),
		cel.Variable("metrics.request_rate_per_sec", cel.DoubleType),
		cel.Variable("metrics.latency_p99_ms", cel.DoubleType),
		cel.Variable("metrics.error_rate_pct", cel.DoubleType),
		cel.Variable("metrics.queue_depth", cel.DoubleType),

		// Time variables
		cel.Variable("time.hour", cel.IntType),
		cel.Variable("time.weekday", cel.IntType), // 0=Sunday
		cel.Variable("time.minute", cel.IntType),

		// Workload state
		cel.Variable("workload.current_replicas", cel.IntType),
		cel.Variable("workload.min_replicas", cel.IntType),
		cel.Variable("workload.max_replicas", cel.IntType),
		cel.Variable("workload.optimization_mode", cel.StringType),

		// SLO state
		cel.Variable("slo.burn_rate_1h", cel.DoubleType),
		cel.Variable("slo.budget_remaining_pct", cel.DoubleType),

		// Cost state
		cel.Variable("cost.hourly_usd", cel.DoubleType),
		cel.Variable("cost.monthly_usd", cel.DoubleType),
		cel.Variable("cost.budget_used_pct", cel.DoubleType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	return &CELEvaluator{env: env}, nil
}

// MetricsContext holds the runtime values for CEL evaluation
type MetricsContext struct {
	CPUPct         float64
	MemoryPct      float64
	RequestRate    float64
	LatencyP99Ms   float64
	ErrorRatePct   float64
	QueueDepth     float64
	CurrentReplicas int
	MinReplicas    int
	MaxReplicas    int
	OptMode        string
	BurnRate1h     float64
	BudgetRemPct   float64
	CostHourly     float64
	CostMonthly    float64
	BudgetUsedPct  float64
}

// EvaluateRule evaluates a single CEL scaling rule against the current metrics
func (e *CELEvaluator) EvaluateRule(rule domain.ScalingRule, mctx MetricsContext) (bool, error) {
	ast, issues := e.env.Compile(rule.Condition)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("CEL compile error for rule %q: %w", rule.Name, issues.Err())
	}

	prg, err := e.env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("CEL program error for rule %q: %w", rule.Name, err)
	}

	now := time.Now()
	activation := map[string]interface{}{
		"metrics.cpu_pct":             mctx.CPUPct,
		"metrics.memory_pct":          mctx.MemoryPct,
		"metrics.request_rate_per_sec": mctx.RequestRate,
		"metrics.latency_p99_ms":      mctx.LatencyP99Ms,
		"metrics.error_rate_pct":      mctx.ErrorRatePct,
		"metrics.queue_depth":         mctx.QueueDepth,

		"time.hour":    int64(now.Hour()),
		"time.weekday": int64(now.Weekday()),
		"time.minute":  int64(now.Minute()),

		"workload.current_replicas": int64(mctx.CurrentReplicas),
		"workload.min_replicas":     int64(mctx.MinReplicas),
		"workload.max_replicas":     int64(mctx.MaxReplicas),
		"workload.optimization_mode": mctx.OptMode,

		"slo.burn_rate_1h":          mctx.BurnRate1h,
		"slo.budget_remaining_pct":  mctx.BudgetRemPct,

		"cost.hourly_usd":       mctx.CostHourly,
		"cost.monthly_usd":      mctx.CostMonthly,
		"cost.budget_used_pct":  mctx.BudgetUsedPct,
	}

	out, _, err := prg.Eval(activation)
	if err != nil {
		return false, fmt.Errorf("CEL eval error for rule %q: %w", rule.Name, err)
	}

	if out.Type() == types.BoolType {
		return out.Value().(bool), nil
	}

	return false, fmt.Errorf("CEL rule %q returned non-bool: %v", rule.Name, out.Type())
}

// EvaluateRules evaluates all scaling rules and returns the matching ones
func (e *CELEvaluator) EvaluateRules(rules []domain.ScalingRule, mctx MetricsContext) []domain.ScalingRule {
	var matched []domain.ScalingRule
	for _, rule := range rules {
		result, err := e.EvaluateRule(rule, mctx)
		if err != nil {
			logger.Error("CEL evaluation error: %v", err)
			continue
		}
		if result {
			matched = append(matched, rule)
		}
	}
	return matched
}

// ParseScalingRules parses JSON-encoded scaling rules from a policy
func ParseScalingRules(rulesJSON string) ([]domain.ScalingRule, error) {
	if rulesJSON == "" {
		return nil, nil
	}
	var rules []domain.ScalingRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil, fmt.Errorf("failed to parse scaling rules: %w", err)
	}
	return rules, nil
}
