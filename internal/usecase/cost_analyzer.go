package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// CostAnalyzer handles cost tracking, budgets, and optimization recommendations
type CostAnalyzer struct {
	costRepo     *postgres.CostRepository
	workloadRepo *postgres.WorkloadRepository
	publisher    events.EventPublisher
}

func NewCostAnalyzer(
	costRepo *postgres.CostRepository,
	workloadRepo *postgres.WorkloadRepository,
	publisher events.EventPublisher,
) *CostAnalyzer {
	return &CostAnalyzer{
		costRepo:     costRepo,
		workloadRepo: workloadRepo,
		publisher:    publisher,
	}
}

// GetCostReport generates a cost report for the given scope
func (a *CostAnalyzer) GetCostReport(ctx context.Context, orgID uuid.UUID, scope, scopeID, window string) (*domain.CostReport, error) {
	workloads, err := a.workloadRepo.FindByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var totalCost float64
	var breakdown []domain.CostBreakdown

	for _, w := range workloads {
		cost := w.MonthlyCostUSD
		totalCost += cost

		wastedUSD := 0.0
		if w.CPUUtilizationPct < 30 && w.MemoryUtilizationPct < 40 {
			wastedUSD = cost * 0.4 // ~40% wasted if severely underutilized
		}

		breakdown = append(breakdown, domain.CostBreakdown{
			WorkloadID:       w.ID.String(),
			WorkloadName:     w.Name,
			CostUSD:          cost,
			WastedUSD:        wastedUSD,
			OptimizationMode: string(w.OptimizationMode),
		})
	}

	// Compute percentages
	for i := range breakdown {
		if totalCost > 0 {
			breakdown[i].CostPct = (breakdown[i].CostUSD / totalCost) * 100
		}
	}

	// Get budget
	budget, _ := a.costRepo.FindBudget(ctx, "organization", orgID.String())
	var budgetUSD, budgetUsedPct float64
	if budget != nil {
		budgetUSD = budget.BudgetUSD
		if budgetUSD > 0 {
			budgetUsedPct = (totalCost / budgetUSD) * 100
		}
	}

	// Generate savings opportunities
	savings := a.findSavingsOpportunities(workloads)

	return &domain.CostReport{
		Scope:                scope,
		Window:               window,
		TotalUSD:             totalCost,
		ProjectedMonthlyUSD:  totalCost, // simplified: current = projected
		BudgetUSD:            budgetUSD,
		BudgetUsedPct:        budgetUsedPct,
		Breakdown:            breakdown,
		SavingsOpportunities: savings,
	}, nil
}

// SetBudget creates or updates a cost budget
func (a *CostAnalyzer) SetBudget(ctx context.Context, orgID uuid.UUID, scope, scopeID string, budgetUSD float64, alertPcts string) error {
	existing, _ := a.costRepo.FindBudget(ctx, scope, scopeID)
	if existing != nil {
		existing.BudgetUSD = budgetUSD
		existing.AlertPcts = alertPcts
		return a.costRepo.UpdateBudget(ctx, existing)
	}

	budget := &domain.CostBudget{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Scope:          scope,
		ScopeID:        scopeID,
		BudgetUSD:      budgetUSD,
		AlertPcts:      alertPcts,
		Enabled:        true,
	}
	return a.costRepo.CreateBudget(ctx, budget)
}

// CheckBudgets evaluates all budgets and publishes threshold events
func (a *CostAnalyzer) CheckBudgets(ctx context.Context) error {
	// For each organization, check budgets against current spend
	workloads, err := a.workloadRepo.FindAllManaged(ctx)
	if err != nil {
		return err
	}

	// Group spend by organization
	orgSpend := make(map[uuid.UUID]float64)
	for _, w := range workloads {
		orgSpend[w.OrganizationID] += w.MonthlyCostUSD
	}

	for orgID, spend := range orgSpend {
		budgets, err := a.costRepo.FindBudgetsByOrg(ctx, orgID)
		if err != nil {
			logger.Error("Failed to fetch budgets for org %s: %v", orgID, err)
			continue
		}

		for _, b := range budgets {
			if b.BudgetUSD <= 0 {
				continue
			}
			usedPct := (spend / b.BudgetUSD) * 100

			if usedPct >= 90 {
				_ = a.publisher.Publish(ctx, events.EventCostThresholdExceeded, events.EventData{
					ResourceID:   b.ScopeID,
					ResourceType: "cost_budget",
					Metadata: map[string]any{
						"scope":         b.Scope,
						"scope_id":      b.ScopeID,
						"budget_usd":    b.BudgetUSD,
						"actual_usd":    spend,
						"projected_usd": spend,
						"threshold_pct": 90,
					},
				})
			}

			b.CurrentSpendUSD = spend
			_ = a.costRepo.UpdateBudget(ctx, b)
		}
	}
	return nil
}

// GetIdleResources returns idle resources for an organization
func (a *CostAnalyzer) GetIdleResources(ctx context.Context, orgID uuid.UUID, resourceType string, minIdleDays int) ([]*domain.IdleResource, error) {
	return a.costRepo.FindIdleResources(ctx, orgID, resourceType, minIdleDays)
}

func (a *CostAnalyzer) findSavingsOpportunities(workloads []*domain.Workload) []domain.SavingsOpportunity {
	var savings []domain.SavingsOpportunity

	var rightsizeCandidates []string
	var spotCandidates []string

	for _, w := range workloads {
		// Rightsizing: low utilization
		if w.CPUUtilizationPct < 30 && w.MemoryUtilizationPct < 40 && w.MonthlyCostUSD > 10 {
			rightsizeCandidates = append(rightsizeCandidates, w.ID.String())
		}

		// Spot candidates: not already on spot, not in performance mode
		if w.OptimizationMode != domain.OptimizationModePerformance && w.MonthlyCostUSD > 50 {
			spotCandidates = append(spotCandidates, w.ID.String())
		}
	}

	if len(rightsizeCandidates) > 0 {
		savings = append(savings, domain.SavingsOpportunity{
			Type:                       domain.SavingsTypeRightsizing,
			Description:                fmt.Sprintf("%d workloads with CPU <30%% and memory <40%% — eligible for rightsizing", len(rightsizeCandidates)),
			EstimatedMonthlySavingsUSD: float64(len(rightsizeCandidates)) * 30,
			Effort:                     "low",
			WorkloadIDs:                rightsizeCandidates,
		})
	}

	if len(spotCandidates) > 0 {
		savings = append(savings, domain.SavingsOpportunity{
			Type:                       domain.SavingsTypeSpot,
			Description:                fmt.Sprintf("%d workloads eligible for spot instance migration — ~60%% cost reduction", len(spotCandidates)),
			EstimatedMonthlySavingsUSD: float64(len(spotCandidates)) * 50,
			Effort:                     "auto",
			WorkloadIDs:                spotCandidates,
		})
	}

	return savings
}
