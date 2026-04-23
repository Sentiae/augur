package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// RIRecommender analyzes usage history and recommends Reserved Instance / Savings Plan purchases.
// Strategy:
//   - P10 usage → Standard RIs (highest discount, least flexible)
//   - P30-P60 → Convertible RIs or EC2 Savings Plans
//   - P60-P80 → Compute Savings Plans (most flexible)
//   - Target: 70-85% coverage to balance savings against flexibility risk
type RIRecommender struct {
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
}

func NewRIRecommender(
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
) *RIRecommender {
	return &RIRecommender{
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
	}
}

// CommitmentType represents a reservation or savings plan type
type CommitmentType string

const (
	CommitmentStandardRI     CommitmentType = "standard_ri"
	CommitmentConvertibleRI  CommitmentType = "convertible_ri"
	CommitmentEC2SavingsPlan CommitmentType = "ec2_savings_plan"
	CommitmentComputeSavings CommitmentType = "compute_savings_plan"
	CommitmentOnDemand       CommitmentType = "on_demand"
)

// RIRecommendation represents a commitment purchase recommendation
type RIRecommendation struct {
	Type               CommitmentType `json:"type"`
	Description        string         `json:"description"`
	HourlyCommitUSD    float64        `json:"hourly_commit_usd"`
	MonthlyCommitUSD   float64        `json:"monthly_commit_usd"`
	DiscountPct        float64        `json:"discount_pct"`
	EstMonthlySavings  float64        `json:"est_monthly_savings_usd"`
	CoveragePct        float64        `json:"coverage_pct"` // what % of usage this covers
	BreakevenMonths    int            `json:"breakeven_months"`
	Risk               string         `json:"risk"` // low, medium, high
}

// RIPortfolio represents the complete commitment recommendation
type RIPortfolio struct {
	OrganizationID     string              `json:"organization_id"`
	AnalysisWindow     string              `json:"analysis_window"`
	CurrentMonthlyUSD  float64             `json:"current_monthly_usd"`
	RecommendedMonthly float64             `json:"recommended_monthly_usd"`
	TotalSavingsUSD    float64             `json:"total_savings_usd"`
	CoveragePct        float64             `json:"total_coverage_pct"`
	Recommendations    []RIRecommendation  `json:"recommendations"`
	UsagePercentiles   UsagePercentiles    `json:"usage_percentiles"`
}

type UsagePercentiles struct {
	P10HourlyUSD float64 `json:"p10_hourly_usd"`
	P30HourlyUSD float64 `json:"p30_hourly_usd"`
	P50HourlyUSD float64 `json:"p50_hourly_usd"`
	P60HourlyUSD float64 `json:"p60_hourly_usd"`
	P80HourlyUSD float64 `json:"p80_hourly_usd"`
	P90HourlyUSD float64 `json:"p90_hourly_usd"`
}

// Recommend generates a commitment portfolio recommendation for an organization
func (r *RIRecommender) Recommend(ctx context.Context, orgID uuid.UUID, windowDays int) (*RIPortfolio, error) {
	if windowDays <= 0 {
		windowDays = 30
	}

	workloads, err := r.workloadRepo.FindByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Collect hourly cost samples across the analysis window
	since := time.Now().Add(-time.Duration(windowDays) * 24 * time.Hour)
	var hourlyCosts []float64

	for _, w := range workloads {
		metrics, err := r.metricsRepo.FindByWorkload(ctx, w.ID, since, 10000)
		if err != nil {
			continue
		}
		for _, m := range metrics {
			hourlyCosts = append(hourlyCosts, m.CostUSDPerHour)
		}
	}

	if len(hourlyCosts) < 100 {
		logger.Debug("RI recommender: insufficient data for org %s (%d samples)", orgID, len(hourlyCosts))
		return &RIPortfolio{
			OrganizationID: orgID.String(),
			AnalysisWindow: fmt.Sprintf("%d days", windowDays),
		}, nil
	}

	sort.Float64s(hourlyCosts)

	// Compute percentiles
	pctls := UsagePercentiles{
		P10HourlyUSD: percentile(hourlyCosts, 10),
		P30HourlyUSD: percentile(hourlyCosts, 30),
		P50HourlyUSD: percentile(hourlyCosts, 50),
		P60HourlyUSD: percentile(hourlyCosts, 60),
		P80HourlyUSD: percentile(hourlyCosts, 80),
		P90HourlyUSD: percentile(hourlyCosts, 90),
	}

	currentMonthly := pctls.P50HourlyUSD * 730 // average hours per month

	// Build recommendations
	var recs []RIRecommendation
	totalSavings := 0.0
	totalCommit := 0.0

	// Tier 1: Standard RIs for baseline (P10) — 40% discount
	if pctls.P10HourlyUSD > 0.01 {
		commit := pctls.P10HourlyUSD
		savings := commit * 0.40 * 730
		recs = append(recs, RIRecommendation{
			Type:              CommitmentStandardRI,
			Description:       "Standard Reserved Instances for baseline usage (P10)",
			HourlyCommitUSD:   commit,
			MonthlyCommitUSD:  commit * 730,
			DiscountPct:       40,
			EstMonthlySavings: savings,
			CoveragePct:       (commit / pctls.P50HourlyUSD) * 100,
			BreakevenMonths:   7,
			Risk:              "low",
		})
		totalSavings += savings
		totalCommit += commit
	}

	// Tier 2: Convertible RIs for P10-P50 band — 30% discount
	band2 := pctls.P50HourlyUSD - pctls.P10HourlyUSD
	if band2 > 0.01 {
		commit := band2 * 0.6 // commit to 60% of this band
		savings := commit * 0.30 * 730
		recs = append(recs, RIRecommendation{
			Type:              CommitmentConvertibleRI,
			Description:       "Convertible RIs for mid-range usage (P10-P50)",
			HourlyCommitUSD:   commit,
			MonthlyCommitUSD:  commit * 730,
			DiscountPct:       30,
			EstMonthlySavings: savings,
			CoveragePct:       (commit / pctls.P50HourlyUSD) * 100,
			BreakevenMonths:   9,
			Risk:              "medium",
		})
		totalSavings += savings
		totalCommit += commit
	}

	// Tier 3: Compute Savings Plans for P50-P80 band — 20% discount
	band3 := pctls.P80HourlyUSD - pctls.P50HourlyUSD
	if band3 > 0.01 {
		commit := band3 * 0.5 // commit to 50% of this band
		savings := commit * 0.20 * 730
		recs = append(recs, RIRecommendation{
			Type:              CommitmentComputeSavings,
			Description:       "Compute Savings Plans for variable usage (P50-P80)",
			HourlyCommitUSD:   commit,
			MonthlyCommitUSD:  commit * 730,
			DiscountPct:       20,
			EstMonthlySavings: savings,
			CoveragePct:       (commit / pctls.P50HourlyUSD) * 100,
			BreakevenMonths:   12,
			Risk:              "low",
		})
		totalSavings += savings
		totalCommit += commit
	}

	coveragePct := 0.0
	if pctls.P50HourlyUSD > 0 {
		coveragePct = math.Min(100, (totalCommit/pctls.P50HourlyUSD)*100)
	}

	return &RIPortfolio{
		OrganizationID:     orgID.String(),
		AnalysisWindow:     fmt.Sprintf("%d days", windowDays),
		CurrentMonthlyUSD:  math.Round(currentMonthly*100) / 100,
		RecommendedMonthly: math.Round((currentMonthly-totalSavings)*100) / 100,
		TotalSavingsUSD:    math.Round(totalSavings*100) / 100,
		CoveragePct:        math.Round(coveragePct*10) / 10,
		Recommendations:    recs,
		UsagePercentiles:   pctls,
	}, nil
}
