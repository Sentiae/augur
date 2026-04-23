package agent

import (
	"testing"
)

func TestRuntimeCollector_Collect(t *testing.T) {
	collector := NewRuntimeCollector()
	snapshot := collector.Collect()

	// CPU proxy should be some positive value
	if snapshot.CPUPct < 0 {
		t.Errorf("CPUPct should be >= 0, got %.2f", snapshot.CPUPct)
	}

	// Memory should be between 0-100%
	if snapshot.MemoryPct < 0 || snapshot.MemoryPct > 100 {
		t.Errorf("MemoryPct should be 0-100, got %.2f", snapshot.MemoryPct)
	}

	if snapshot.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestMetricsSnapshot_Fields(t *testing.T) {
	snap := MetricsSnapshot{
		CPUPct:         75.5,
		MemoryPct:      60.2,
		RequestsPerSec: 1500,
		LatencyP99Ms:   250,
		ErrorRatePct:   0.5,
		ReplicaCount:   3,
	}

	if snap.CPUPct != 75.5 {
		t.Errorf("CPUPct mismatch")
	}
	if snap.ReplicaCount != 3 {
		t.Errorf("ReplicaCount mismatch")
	}
}
