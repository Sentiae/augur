package agent

import (
	"testing"
)

func TestDegradedMode_EnterExit(t *testing.T) {
	dm := NewDegradedMode(10)

	if dm.IsActive() {
		t.Error("should not be active initially")
	}

	dm.Enter()
	if !dm.IsActive() {
		t.Error("should be active after Enter")
	}

	decisions := dm.Exit()
	if dm.IsActive() {
		t.Error("should not be active after Exit")
	}
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions, got %d", len(decisions))
	}
}

func TestDegradedMode_ScaleUpOnCriticalCPU(t *testing.T) {
	dm := NewDegradedMode(10)
	dm.Enter()

	metrics := MetricsSnapshot{
		CPUPct:    95, // critical: > 90
		MemoryPct: 50,
	}

	target, shouldScale := dm.EvaluateScaleUp(metrics, 3)
	if !shouldScale {
		t.Error("should recommend scale-up on critical CPU")
	}
	if target != 4 {
		t.Errorf("expected target=4, got %d", target)
	}
}

func TestDegradedMode_ScaleUpOnCriticalMemory(t *testing.T) {
	dm := NewDegradedMode(10)
	dm.Enter()

	metrics := MetricsSnapshot{
		CPUPct:    50,
		MemoryPct: 90, // critical: > 85
	}

	target, shouldScale := dm.EvaluateScaleUp(metrics, 5)
	if !shouldScale {
		t.Error("should recommend scale-up on critical memory")
	}
	if target != 6 {
		t.Errorf("expected target=6, got %d", target)
	}
}

func TestDegradedMode_NoScaleUpBelowThreshold(t *testing.T) {
	dm := NewDegradedMode(10)
	dm.Enter()

	metrics := MetricsSnapshot{
		CPUPct:    70, // not critical
		MemoryPct: 60, // not critical
	}

	_, shouldScale := dm.EvaluateScaleUp(metrics, 3)
	if shouldScale {
		t.Error("should NOT scale up when below critical thresholds")
	}
}

func TestDegradedMode_RespectsMaxReplicas(t *testing.T) {
	dm := NewDegradedMode(5)
	dm.Enter()

	metrics := MetricsSnapshot{
		CPUPct:    95,
		MemoryPct: 90,
	}

	_, shouldScale := dm.EvaluateScaleUp(metrics, 5) // already at max
	if shouldScale {
		t.Error("should NOT scale up when at max replicas")
	}
}

func TestDegradedMode_NotActiveNoAction(t *testing.T) {
	dm := NewDegradedMode(10)
	// Not entering degraded mode

	metrics := MetricsSnapshot{CPUPct: 95, MemoryPct: 90}
	_, shouldScale := dm.EvaluateScaleUp(metrics, 3)
	if shouldScale {
		t.Error("should not scale when degraded mode is not active")
	}
}

func TestDegradedMode_DecisionsRecorded(t *testing.T) {
	dm := NewDegradedMode(10)
	dm.Enter()

	metrics := MetricsSnapshot{CPUPct: 95, MemoryPct: 90}
	dm.EvaluateScaleUp(metrics, 3)
	dm.EvaluateScaleUp(metrics, 4)

	decisions := dm.Exit()
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(decisions))
	}
	if decisions[0].FromRep != 3 || decisions[0].ToRep != 4 {
		t.Errorf("decision[0]: expected 3→4, got %d→%d", decisions[0].FromRep, decisions[0].ToRep)
	}
	if decisions[1].FromRep != 4 || decisions[1].ToRep != 5 {
		t.Errorf("decision[1]: expected 4→5, got %d→%d", decisions[1].FromRep, decisions[1].ToRep)
	}
}

func TestDegradedMode_UpdateMaxReplicas(t *testing.T) {
	dm := NewDegradedMode(3)
	dm.Enter()

	metrics := MetricsSnapshot{CPUPct: 95}
	_, shouldScale := dm.EvaluateScaleUp(metrics, 3)
	if shouldScale {
		t.Error("should not scale when at original max")
	}

	dm.UpdateMaxReplicas(5)
	_, shouldScale = dm.EvaluateScaleUp(metrics, 3)
	if !shouldScale {
		t.Error("should scale after max replicas increased")
	}
}
