package agent

import (
	"sync"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// DegradedMode implements the autonomy rules when the edge agent loses connectivity.
// Rules:
//   - Scale UP only on critical pressure (CPU > 90%, memory > 85%)
//   - Never scale DOWN
//   - Never execute cost-optimization changes
//   - Respect the last cached policy ceiling as max replicas
//   - Log all autonomous decisions with timestamps
type DegradedMode struct {
	mu             sync.RWMutex
	active         bool
	disconnectedAt time.Time
	maxReplicas    int
	decisions      []DegradedDecision
}

type DegradedDecision struct {
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason"`
	FromRep   int       `json:"from_replicas"`
	ToRep     int       `json:"to_replicas"`
}

func NewDegradedMode(maxReplicas int) *DegradedMode {
	return &DegradedMode{maxReplicas: maxReplicas}
}

// Enter activates degraded mode
func (d *DegradedMode) Enter() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active = true
	d.disconnectedAt = time.Now()
	logger.Warn("Entering degraded mode — limited autonomous scaling only")
}

// Exit deactivates degraded mode and returns decisions made during disconnection
func (d *DegradedMode) Exit() []DegradedDecision {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active = false
	decisions := d.decisions
	d.decisions = nil
	logger.Info("Exiting degraded mode — %d autonomous decisions to reconcile", len(decisions))
	return decisions
}

// IsActive returns whether degraded mode is currently active
func (d *DegradedMode) IsActive() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.active
}

// EvaluateScaleUp checks if an emergency scale-up should be executed while disconnected.
// Only permits scale-up on critical pressure, never scale-down.
func (d *DegradedMode) EvaluateScaleUp(metrics MetricsSnapshot, currentReplicas int) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.active {
		return currentReplicas, false
	}

	// Only scale up on critical pressure
	criticalCPU := metrics.CPUPct > 90
	criticalMemory := metrics.MemoryPct > 85

	if !criticalCPU && !criticalMemory {
		return currentReplicas, false
	}

	target := currentReplicas + 1
	if target > d.maxReplicas {
		logger.Warn("Degraded mode: would scale up but at max replicas (%d)", d.maxReplicas)
		return currentReplicas, false
	}

	reason := "critical pressure"
	if criticalCPU {
		reason = "CPU > 90%"
	}
	if criticalMemory {
		reason = "memory > 85%"
	}
	if criticalCPU && criticalMemory {
		reason = "CPU > 90% AND memory > 85%"
	}

	decision := DegradedDecision{
		Timestamp: time.Now(),
		Action:    "scale_up",
		Reason:    reason,
		FromRep:   currentReplicas,
		ToRep:     target,
	}
	d.decisions = append(d.decisions, decision)

	logger.Warn("Degraded mode: autonomous scale-up %d→%d (%s)", currentReplicas, target, reason)
	return target, true
}

// UpdateMaxReplicas updates the cached policy ceiling
func (d *DegradedMode) UpdateMaxReplicas(max int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maxReplicas = max
}
