package usecase

import (
	"context"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// OutcomeObserver checks pending scaling decisions and evaluates their outcomes
type OutcomeObserver struct {
	decisionRepo *postgres.DecisionRepository
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
	publisher    events.EventPublisher
	orgLister    OrgLister
	rollbackMin  int
}

func NewOutcomeObserver(
	decisionRepo *postgres.DecisionRepository,
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
	publisher events.EventPublisher,
	orgLister OrgLister,
	rollbackMin int,
) *OutcomeObserver {
	return &OutcomeObserver{
		decisionRepo: decisionRepo,
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
		publisher:    publisher,
		orgLister:    orgLister,
		rollbackMin:  rollbackMin,
	}
}

// Run starts the outcome observer loop (checks every 60s)
func (o *OutcomeObserver) Run(ctx context.Context) {
	logger.Info("Outcome observer started (rollback window=%dmin)", o.rollbackMin)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Outcome observer stopped")
			return
		case <-ticker.C:
			o.checkPendingOutcomes(ctx)
		}
	}
}

func (o *OutcomeObserver) checkPendingOutcomes(ctx context.Context) {
	_ = ForEachOrg(ctx, o.orgLister, func(orgCtx context.Context) error {
		// Find decisions that are still pending and older than the rollback window
		cutoff := time.Now().Add(-time.Duration(o.rollbackMin) * time.Minute)
		decisions, err := o.decisionRepo.FindPendingOutcomes(orgCtx, cutoff)
		if err != nil {
			logger.Error("Outcome observer: failed to fetch pending decisions: %v", err)
			return err
		}

		for _, d := range decisions {
			o.evaluateOutcome(orgCtx, d)
		}
		return nil
	})
}

func (o *OutcomeObserver) evaluateOutcome(ctx context.Context, d *domain.ScalingDecision) {
	w, err := o.workloadRepo.FindByID(ctx, d.WorkloadID)
	if err != nil {
		logger.Error("Outcome observer: workload %s not found: %v", d.WorkloadID, err)
		return
	}

	outcome := domain.DecisionOutcomeHealthy

	// Check if SLO degraded after the action
	if w.ErrorRatePct > 2.0 {
		outcome = domain.DecisionOutcomeDegraded
		logger.Warn("Outcome observer: workload %s degraded (error rate %.2f%%) after decision %s",
			w.Name, w.ErrorRatePct, d.ID)
	}

	// Check for crash loops (consecutive failures)
	if w.ConsecutiveFailures > 0 {
		outcome = domain.DecisionOutcomeFailed
	}

	// Record the outcome
	now := time.Now()
	d.Outcome = outcome
	d.OutcomeObserved = &now

	if err := o.decisionRepo.Update(ctx, d); err != nil {
		logger.Error("Outcome observer: failed to update decision %s: %v", d.ID, err)
		return
	}

	// If degraded, publish rollback event and update workload
	if outcome == domain.DecisionOutcomeDegraded {
		_ = o.publisher.Publish(ctx, events.EventScaleRolledBack, events.EventData{
			ResourceID:   w.ID.String(),
			ResourceType: "workload",
			Metadata: map[string]any{
				"workload_name":    w.Name,
				"current_replicas": w.CurrentReplicas,
				"target_replicas":  d.FromReplicas,
				"scale_direction":  "rollback",
				"trigger":          "safety",
				"reasoning":        "SLO degradation detected post-scaling — automatic rollback",
			},
		})

		// Revert the workload to pre-action state
		w.DesiredReplicas = d.FromReplicas
		w.Status = domain.WorkloadStatusHealthy
		if err := o.workloadRepo.Update(ctx, w); err != nil {
			logger.Error("Outcome observer: failed to rollback workload %s: %v", w.Name, err)
		} else {
			logger.Info("Outcome observer: rolled back workload %s to %d replicas", w.Name, d.FromReplicas)
		}
	}

	// If healthy, update workload to reflect new state
	if outcome == domain.DecisionOutcomeHealthy {
		w.CurrentReplicas = d.ToReplicas
		w.Status = domain.WorkloadStatusHealthy
		w.ConsecutiveFailures = 0
		scaledAt := time.Now()
		w.LastScaledAt = &scaledAt
		_ = o.workloadRepo.Update(ctx, w)

		_ = o.publisher.Publish(ctx, events.EventScaleCompleted, events.EventData{
			ResourceID:   w.ID.String(),
			ResourceType: "workload",
			Metadata: map[string]any{
				"workload_name":    w.Name,
				"current_replicas": d.FromReplicas,
				"target_replicas":  d.ToReplicas,
				"scale_direction":  string(d.Direction),
				"trigger":          string(d.Trigger),
				"reasoning":        d.Reasoning,
			},
		})
	}
}
