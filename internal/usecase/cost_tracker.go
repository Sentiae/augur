package usecase

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// CostTracker runs continuous cost tracking and budget enforcement
type CostTracker struct {
	costRepo     *postgres.CostRepository
	workloadRepo *postgres.WorkloadRepository
	publisher    events.EventPublisher
	orgLister    OrgLister
}

func NewCostTracker(
	costRepo *postgres.CostRepository,
	workloadRepo *postgres.WorkloadRepository,
	publisher events.EventPublisher,
	orgLister OrgLister,
) *CostTracker {
	return &CostTracker{
		costRepo:     costRepo,
		workloadRepo: workloadRepo,
		publisher:    publisher,
		orgLister:    orgLister,
	}
}

// Run starts the continuous cost tracking loop (every 5 minutes)
func (t *CostTracker) Run(ctx context.Context) {
	logger.Info("Cost tracker started (interval=5m)")
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Cost tracker stopped")
			return
		case <-ticker.C:
			t.tick(ctx)
		}
	}
}

func (t *CostTracker) tick(ctx context.Context) {
	// Update hourly costs for all workloads
	t.updateWorkloadCosts(ctx)

	// Check all budgets against current spend
	t.enforceBudgets(ctx)
}

// updateWorkloadCosts computes hourly and monthly cost for each workload based on its type and replicas
func (t *CostTracker) updateWorkloadCosts(ctx context.Context) {
	_ = ForEachOrg(ctx, t.orgLister, func(orgCtx context.Context) error {
		workloads, err := t.workloadRepo.FindAllManaged(orgCtx)
		if err != nil {
			logger.Error("Cost tracker: failed to fetch workloads: %v", err)
			return err
		}

		for _, w := range workloads {
			hourly := computeHourlyCost(w)
			monthly := hourly * 730 // average hours per month

			w.HourlyCostUSD = hourly
			w.MonthlyCostUSD = monthly

			if err := t.workloadRepo.Update(orgCtx, w); err != nil {
				logger.Error("Cost tracker: failed to update cost for %s: %v", w.Name, err)
			}
		}
		return nil
	})
}

// computeHourlyCost estimates hourly cost based on workload type and replicas
func computeHourlyCost(w *domain.Workload) float64 {
	replicas := w.CurrentReplicas
	if replicas < 1 {
		replicas = 1
	}

	// Per-replica hourly cost by workload type
	var perReplicaHourly float64
	switch w.WorkloadType {
	case domain.WorkloadTypeFirecracker:
		// Firecracker microVM: ~$0.015/hr per vCPU equivalent
		perReplicaHourly = 0.015
	case domain.WorkloadTypeKubernetes:
		// K8s pod: ~$0.04/hr (0.25 vCPU + 512MB default request)
		perReplicaHourly = 0.04
	case domain.WorkloadTypeVM:
		// VM instance: ~$0.10/hr (t3.small equivalent)
		perReplicaHourly = 0.10
	case domain.WorkloadTypeServerless:
		// Serverless: cost per invocation, estimate from request rate
		// ~$0.0000002 per invocation + $0.00001667 per GB-second
		perReplicaHourly = w.RequestsPerSec * 3600 * 0.0000002
		if perReplicaHourly < 0.001 {
			perReplicaHourly = 0.001 // minimum
		}
		return perReplicaHourly // serverless doesn't scale by replicas
	default:
		perReplicaHourly = 0.05
	}

	return perReplicaHourly * float64(replicas)
}

// enforceBudgets checks budgets and publishes threshold events
func (t *CostTracker) enforceBudgets(ctx context.Context) {
	_ = ForEachOrg(ctx, t.orgLister, func(orgCtx context.Context) error {
		workloads, err := t.workloadRepo.FindAllManaged(orgCtx)
		if err != nil {
			return err
		}

		// Group spend by organization
		orgSpend := make(map[uuid.UUID]float64)
		for _, w := range workloads {
			orgSpend[w.OrganizationID] += w.MonthlyCostUSD
		}

		for orgID, spend := range orgSpend {
			budgets, err := t.costRepo.FindBudgetsByOrg(orgCtx, orgID)
			if err != nil {
				continue
			}

			for _, b := range budgets {
				if b.BudgetUSD <= 0 {
					continue
				}

				usedPct := (spend / b.BudgetUSD) * 100
				b.CurrentSpendUSD = spend
				_ = t.costRepo.UpdateBudget(orgCtx, b)

				// Parse alert thresholds
				thresholds := parseAlertPcts(b.AlertPcts)

				for _, threshold := range thresholds {
					if usedPct >= float64(threshold) {
						t.publishThresholdEvent(orgCtx, b, spend, threshold)
					}
				}

				// At 100%: force cost mode on all workloads in this org
				if usedPct >= 100 {
					t.forceCostMode(orgCtx, orgID, workloads)
				}
			}
		}
		return nil
	})
}

func (t *CostTracker) publishThresholdEvent(ctx context.Context, b *domain.CostBudget, actual float64, threshold int) {
	_ = t.publisher.Publish(ctx, events.EventCostThresholdExceeded, events.EventData{
		ResourceID:   b.ScopeID,
		ResourceType: "cost_budget",
		Metadata: map[string]any{
			"scope":         b.Scope,
			"scope_id":      b.ScopeID,
			"budget_usd":    b.BudgetUSD,
			"actual_usd":    actual,
			"projected_usd": actual,
			"threshold_pct": threshold,
		},
	})
}

// forceCostMode switches all org workloads to cost optimization when budget is exceeded
func (t *CostTracker) forceCostMode(ctx context.Context, orgID uuid.UUID, workloads []*domain.Workload) {
	for _, w := range workloads {
		if w.OrganizationID != orgID {
			continue
		}
		if w.OptimizationMode == domain.OptimizationModeCost {
			continue
		}

		logger.Warn("Budget exceeded for org %s — forcing workload %s to cost mode", orgID, w.Name)
		w.OptimizationMode = domain.OptimizationModeCost
		_ = t.workloadRepo.Update(ctx, w)
	}
}

func parseAlertPcts(s string) []int {
	if s == "" {
		return []int{80, 90, 100}
	}
	var pcts []int
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil {
			pcts = append(pcts, v)
		}
	}
	if len(pcts) == 0 {
		return []int{80, 90, 100}
	}
	return pcts
}
