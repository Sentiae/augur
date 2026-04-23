package usecase

import (
	"testing"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

func TestCELEvaluator_HighTrafficRule(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to create CEL evaluator: %v", err)
	}

	rule := domain.ScalingRule{
		Name:      "High traffic scale-up",
		Condition: "metrics.request_rate_per_sec > 5000 && metrics.latency_p99_ms > 150",
		Action:    "scale_up",
		TargetReplicasPct: 150,
	}

	// Should match: high traffic + high latency
	match, err := eval.EvaluateRule(rule, MetricsContext{
		RequestRate:  6000,
		LatencyP99Ms: 200,
	})
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	if !match {
		t.Error("expected rule to match for high traffic + high latency")
	}

	// Should NOT match: high traffic but low latency
	match, err = eval.EvaluateRule(rule, MetricsContext{
		RequestRate:  6000,
		LatencyP99Ms: 100,
	})
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}
	if match {
		t.Error("expected rule NOT to match when latency is low")
	}
}

func TestCELEvaluator_CPUThreshold(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to create CEL evaluator: %v", err)
	}

	rule := domain.ScalingRule{
		Name:      "CPU pressure",
		Condition: "metrics.cpu_pct > 80",
		Action:    "scale_up",
	}

	match, _ := eval.EvaluateRule(rule, MetricsContext{CPUPct: 90})
	if !match {
		t.Error("expected match for CPU > 80")
	}

	match, _ = eval.EvaluateRule(rule, MetricsContext{CPUPct: 50})
	if match {
		t.Error("expected no match for CPU = 50")
	}
}

func TestCELEvaluator_CostBudgetRule(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to create CEL evaluator: %v", err)
	}

	rule := domain.ScalingRule{
		Name:      "Budget alert",
		Condition: "cost.budget_used_pct > 90",
		Action:    "scale_down",
	}

	match, _ := eval.EvaluateRule(rule, MetricsContext{BudgetUsedPct: 95})
	if !match {
		t.Error("expected match for budget > 90%")
	}
}

func TestCELEvaluator_WorkloadStateRule(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to create CEL evaluator: %v", err)
	}

	rule := domain.ScalingRule{
		Name:      "Min replicas when cost mode",
		Condition: `workload.optimization_mode == "cost" && workload.current_replicas > 3`,
		Action:    "set_min_replicas",
		Value:     2,
	}

	match, _ := eval.EvaluateRule(rule, MetricsContext{
		OptMode:         "cost",
		CurrentReplicas: 5,
	})
	if !match {
		t.Error("expected match for cost mode with 5 replicas")
	}

	match, _ = eval.EvaluateRule(rule, MetricsContext{
		OptMode:         "performance",
		CurrentReplicas: 5,
	})
	if match {
		t.Error("expected no match for performance mode")
	}
}

func TestCELEvaluator_InvalidExpression(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to create CEL evaluator: %v", err)
	}

	rule := domain.ScalingRule{
		Name:      "Bad rule",
		Condition: "this is not valid CEL",
		Action:    "scale_up",
	}

	_, err = eval.EvaluateRule(rule, MetricsContext{})
	if err == nil {
		t.Error("expected error for invalid CEL expression")
	}
}

func TestParseScalingRules(t *testing.T) {
	rulesJSON := `[{"name":"test","condition":"metrics.cpu_pct > 80","action":"scale_up","target_replicas_pct":150}]`
	rules, err := ParseScalingRules(rulesJSON)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "test" {
		t.Errorf("expected name 'test', got %q", rules[0].Name)
	}
	if rules[0].TargetReplicasPct != 150 {
		t.Errorf("expected target 150%%, got %d", rules[0].TargetReplicasPct)
	}
}

func TestParseScalingRules_Empty(t *testing.T) {
	rules, err := ParseScalingRules("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rules != nil {
		t.Errorf("expected nil for empty string, got %v", rules)
	}
}
