package domain

import (
	"testing"
)

func TestResolvePolicy_Defaults(t *testing.T) {
	resolved := ResolvePolicy(nil, nil, nil)

	if resolved.OptimizationMode != OptimizationModeBalanced {
		t.Errorf("Default mode: got %v, want balanced", resolved.OptimizationMode)
	}
	if resolved.MinReplicas != 1 {
		t.Errorf("Default minReplicas: got %d, want 1", resolved.MinReplicas)
	}
	if resolved.MaxReplicas != 100 {
		t.Errorf("Default maxReplicas: got %d, want 100", resolved.MaxReplicas)
	}
}

func TestResolvePolicy_ChildOverridesOptimizationMode(t *testing.T) {
	globalMode := OptimizationModeBalanced
	global := &AugurPolicy{Scope: PolicyScopeGlobal, OptimizationMode: &globalMode}

	groupMode := OptimizationModePerformance
	group := &AugurPolicy{Scope: PolicyScopeGroup, OptimizationMode: &groupMode}

	resolved := ResolvePolicy(global, group, nil)
	if resolved.OptimizationMode != OptimizationModePerformance {
		t.Errorf("Group should override global: got %v, want performance", resolved.OptimizationMode)
	}

	appMode := OptimizationModeCost
	app := &AugurPolicy{Scope: PolicyScopeApp, OptimizationMode: &appMode}

	resolved = ResolvePolicy(global, group, app)
	if resolved.OptimizationMode != OptimizationModeCost {
		t.Errorf("App should override group: got %v, want cost", resolved.OptimizationMode)
	}
}

func TestResolvePolicy_SafetyConstraintsMostRestrictive(t *testing.T) {
	globalMin := 2
	globalMax := 50
	globalBudget := 10000.0
	global := &AugurPolicy{MinReplicas: &globalMin, MaxReplicas: &globalMax, MaxBudgetUSD: &globalBudget}

	groupMin := 5
	groupMax := 20
	groupBudget := 5000.0
	group := &AugurPolicy{MinReplicas: &groupMin, MaxReplicas: &groupMax, MaxBudgetUSD: &groupBudget}

	resolved := ResolvePolicy(global, group, nil)

	// MinReplicas: take the maximum (most restrictive)
	if resolved.MinReplicas != 5 {
		t.Errorf("MinReplicas should take max: got %d, want 5", resolved.MinReplicas)
	}

	// MaxReplicas: take the minimum (most restrictive)
	if resolved.MaxReplicas != 20 {
		t.Errorf("MaxReplicas should take min: got %d, want 20", resolved.MaxReplicas)
	}

	// MaxBudgetUSD: take the minimum (most restrictive)
	if resolved.MaxBudgetUSD != 5000.0 {
		t.Errorf("MaxBudgetUSD should take min: got %f, want 5000", resolved.MaxBudgetUSD)
	}
}

func TestResolvePolicy_SpotOverride(t *testing.T) {
	enableSpot := true
	global := &AugurPolicy{EnableSpot: &enableSpot}

	disableSpot := false
	app := &AugurPolicy{EnableSpot: &disableSpot}

	resolved := ResolvePolicy(global, nil, app)
	if resolved.EnableSpot != false {
		t.Errorf("App should override spot: got %v, want false", resolved.EnableSpot)
	}
}
