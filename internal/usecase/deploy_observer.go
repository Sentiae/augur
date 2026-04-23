package usecase

import (
	"context"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// DeployObserver handles post-deploy observation mode for workloads
type DeployObserver struct {
	workloadRepo *postgres.WorkloadRepository
	observeMin   int
}

func NewDeployObserver(workloadRepo *postgres.WorkloadRepository, observeMin int) *DeployObserver {
	return &DeployObserver{
		workloadRepo: workloadRepo,
		observeMin:   observeMin,
	}
}

// OnDeployCompleted puts affected workloads into post-deploy observation mode.
// During this period: no scale-down, wider anomaly sensitivity.
func (d *DeployObserver) OnDeployCompleted(ctx context.Context, environmentID string) error {
	// Find workloads in the affected environment
	workloads, err := d.workloadRepo.FindAllManaged(ctx)
	if err != nil {
		return err
	}

	pauseUntil := time.Now().Add(time.Duration(d.observeMin) * time.Minute)
	affected := 0

	for _, w := range workloads {
		if w.Environment != environmentID {
			continue
		}

		// Don't override a longer existing pause
		if w.AutoscalingPaused && w.PausedUntil != nil && w.PausedUntil.After(pauseUntil) {
			continue
		}

		// Enter post-deploy observation: pause scale-down only
		// We set the workload status to scaling (observation) and pause scale-down
		w.PausedUntil = &pauseUntil
		w.PauseReason = "Post-deploy observation mode"
		w.Status = domain.WorkloadStatusScaling

		if err := d.workloadRepo.Update(ctx, w); err != nil {
			logger.Error("Failed to set post-deploy observation for workload %s: %v", w.Name, err)
			continue
		}
		affected++
	}

	if affected > 0 {
		logger.Info("Post-deploy observation: %d workloads in env %s paused for %d minutes",
			affected, environmentID, d.observeMin)
	}

	return nil
}

// CheckPostDeployExpiry checks workloads whose post-deploy observation period has expired
func (d *DeployObserver) CheckPostDeployExpiry(ctx context.Context) error {
	workloads, err := d.workloadRepo.FindAllManaged(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, w := range workloads {
		if w.PauseReason != "Post-deploy observation mode" {
			continue
		}
		if w.PausedUntil != nil && now.After(*w.PausedUntil) {
			w.PausedUntil = nil
			w.PauseReason = ""
			w.AutoscalingPaused = false
			w.Status = domain.WorkloadStatusHealthy

			if err := d.workloadRepo.Update(ctx, w); err != nil {
				logger.Error("Failed to exit post-deploy observation for workload %s: %v", w.Name, err)
				continue
			}
			logger.Info("Workload %s exited post-deploy observation mode", w.Name)
		}
	}
	return nil
}
