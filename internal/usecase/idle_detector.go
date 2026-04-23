package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// IdleDetector scans for idle resources that should be decommissioned
type IdleDetector struct {
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
	costRepo     *postgres.CostRepository
	publisher    events.EventPublisher
}

func NewIdleDetector(
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
	costRepo *postgres.CostRepository,
	publisher events.EventPublisher,
) *IdleDetector {
	return &IdleDetector{
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
		costRepo:     costRepo,
		publisher:    publisher,
	}
}

// Run starts the idle detection loop (every hour)
func (d *IdleDetector) Run(ctx context.Context) {
	logger.Info("Idle detector started (interval=1h)")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once on startup
	d.scan(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Idle detector stopped")
			return
		case <-ticker.C:
			d.scan(ctx)
		}
	}
}

func (d *IdleDetector) scan(ctx context.Context) {
	workloads, err := d.workloadRepo.FindAllManaged(ctx)
	if err != nil {
		logger.Error("Idle detector: failed to fetch workloads: %v", err)
		return
	}

	for _, w := range workloads {
		d.checkWorkloadIdle(ctx, w)
	}
}

func (d *IdleDetector) checkWorkloadIdle(ctx context.Context, w *domain.Workload) {
	// Idle criteria: CPU < 20% AND memory < 30% for 7+ continuous days
	if w.CPUUtilizationPct >= 20 || w.MemoryUtilizationPct >= 30 {
		return
	}

	// Check historical metrics to confirm sustained low utilization
	since := time.Now().Add(-7 * 24 * time.Hour)
	metrics, err := d.metricsRepo.FindByWorkload(ctx, w.ID, since, 1000)
	if err != nil || len(metrics) < 10 {
		return // not enough data
	}

	// Verify all samples are below threshold
	for _, m := range metrics {
		if m.CPUPct >= 20 || m.MemoryPct >= 30 {
			return // not consistently idle
		}
	}

	// Calculate idle days from the oldest metric below threshold
	idleDays := int(time.Since(metrics[len(metrics)-1].Timestamp).Hours() / 24)
	if idleDays < 7 {
		return
	}

	wasteUSD := w.MonthlyCostUSD

	// Check if already tracked
	existing, _ := d.costRepo.FindIdleResources(ctx, w.OrganizationID, "compute", 0)
	for _, r := range existing {
		if r.ResourceID == w.ID.String() {
			// Update existing
			r.IdleSinceDays = idleDays
			r.EstimatedMonthlyWasteUSD = wasteUSD
			_ = d.costRepo.UpdateIdleResource(ctx, r)
			return
		}
	}

	// Create new idle resource record
	idleRes := &domain.IdleResource{
		ID:                       uuid.New(),
		OrganizationID:           w.OrganizationID,
		ResourceID:               w.ID.String(),
		ResourceType:             "compute",
		Name:                     w.Name,
		Environment:              w.Environment,
		IdleSinceDays:            idleDays,
		EstimatedMonthlyWasteUSD: wasteUSD,
	}

	if err := d.costRepo.CreateIdleResource(ctx, idleRes); err != nil {
		logger.Error("Idle detector: failed to create idle resource for %s: %v", w.Name, err)
		return
	}

	_ = d.publisher.Publish(ctx, events.EventIdleDetected, events.EventData{
		ResourceID:   w.ID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"workload_name": w.Name,
			"idle_days":     idleDays,
			"monthly_waste": fmt.Sprintf("$%.2f", wasteUSD),
			"environment":   w.Environment,
		},
	})

	logger.Info("Idle resource detected: %s (idle %d days, wasting $%.2f/mo)", w.Name, idleDays, wasteUSD)
}
