package usecase

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/infrastructure/workclient"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// SpecCreator automatically creates work-service specs for critical Augur events.
// This bridges infra anomalies/failures into the work-tracking system.
type SpecCreator struct {
	workClient   *workclient.Client
	workloadRepo *postgres.WorkloadRepository
	enabled      bool
}

// NewSpecCreator creates a spec auto-creation handler.
func NewSpecCreator(
	workClient *workclient.Client,
	workloadRepo *postgres.WorkloadRepository,
	enabled bool,
) *SpecCreator {
	return &SpecCreator{
		workClient:   workClient,
		workloadRepo: workloadRepo,
		enabled:      enabled,
	}
}

// OnAnomalyDetected creates a remediation spec when a critical anomaly is detected.
func (s *SpecCreator) OnAnomalyDetected(ctx context.Context, anomaly *domain.AnomalyScore) {
	if !s.enabled || s.workClient == nil {
		return
	}

	// Only create specs for critical anomalies
	if anomaly.Severity != domain.AlertSeverityCritical {
		return
	}

	w, err := s.workloadRepo.FindByID(ctx, anomaly.WorkloadID)
	if err != nil {
		logger.Error("spec-creator: failed to find workload %s: %v", anomaly.WorkloadID, err)
		return
	}

	title := fmt.Sprintf("[Augur] Critical anomaly: %s on %s", anomaly.AnomalyType, w.Name)
	description := fmt.Sprintf(
		"Augur detected a critical anomaly on workload **%s** (%s).\n\n"+
			"**Type:** %s\n"+
			"**Severity:** %s\n"+
			"**Description:** %s\n"+
			"**Suggested Action:** %s\n"+
			"**Confidence:** %.0f%%\n"+
			"**Affected Metrics:** %v",
		w.Name, w.WorkloadType,
		anomaly.AnomalyType,
		anomaly.Severity,
		anomaly.Description,
		anomaly.SuggestedAction,
		anomaly.Confidence*100,
		anomaly.AffectedMetrics,
	)
	why := fmt.Sprintf("Critical infrastructure anomaly detected by Augur ML pipeline on %s environment", w.Environment)

	req := workclient.CreateSpecRequest{
		OrganizationID: w.OrganizationID,
		Title:          title,
		Description:    description,
		Why:            why,
		Priority:       "urgent",
		Status:         "draft",
		CreatedBy:      workclient.SystemUserID,
	}

	// Link to parent spec if workload has one
	if w.SpecID != nil {
		req.ParentSpecID = w.SpecID
	}

	resp, err := s.workClient.CreateSpec(ctx, req)
	if err != nil {
		logger.Error("spec-creator: failed to create spec for anomaly on %s: %v", w.Name, err)
		return
	}

	specID := ""
	if resp != nil {
		specID = resp.ID
	}
	logger.Info("spec-creator: created remediation spec %s for anomaly on %s", specID, w.Name)
}

// OnCircuitBreakerOpened creates a spec when a workload's circuit breaker opens.
func (s *SpecCreator) OnCircuitBreakerOpened(ctx context.Context, workloadID uuid.UUID, failures int, lastReason string) {
	if !s.enabled || s.workClient == nil {
		return
	}

	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		logger.Error("spec-creator: failed to find workload %s: %v", workloadID, err)
		return
	}

	title := fmt.Sprintf("[Augur] Circuit breaker opened: %s", w.Name)
	description := fmt.Sprintf(
		"Augur's circuit breaker opened for workload **%s** after **%d consecutive scaling failures**.\n\n"+
			"**Last failure:** %s\n\n"+
			"Autoscaling is now paused. Manual investigation required.",
		w.Name, failures, lastReason,
	)
	why := "Repeated scaling failures indicate an underlying infrastructure issue that needs human attention"

	req := workclient.CreateSpecRequest{
		OrganizationID: w.OrganizationID,
		Title:          title,
		Description:    description,
		Why:            why,
		Priority:       "high",
		Status:         "draft",
		CreatedBy:      workclient.SystemUserID,
	}
	if w.SpecID != nil {
		req.ParentSpecID = w.SpecID
	}

	resp, err := s.workClient.CreateSpec(ctx, req)
	if err != nil {
		logger.Error("spec-creator: failed to create spec for circuit breaker on %s: %v", w.Name, err)
		return
	}

	specID := ""
	if resp != nil {
		specID = resp.ID
	}
	logger.Info("spec-creator: created spec %s for circuit breaker on %s", specID, w.Name)
}

// OnSLOBudgetExhausted creates a spec when an SLO error budget is fully consumed.
func (s *SpecCreator) OnSLOBudgetExhausted(ctx context.Context, workloadID uuid.UUID, sloType string, burnRate float64) {
	if !s.enabled || s.workClient == nil {
		return
	}

	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		logger.Error("spec-creator: failed to find workload %s: %v", workloadID, err)
		return
	}

	title := fmt.Sprintf("[Augur] SLO budget exhausted: %s (%s)", w.Name, sloType)
	description := fmt.Sprintf(
		"SLO error budget for **%s** on workload **%s** is exhausted.\n\n"+
			"**Burn rate:** %.1fx\n\n"+
			"Immediate reliability improvement required.",
		sloType, w.Name, burnRate,
	)
	why := "SLO error budget exhaustion means the service is no longer meeting its reliability targets"

	req := workclient.CreateSpecRequest{
		OrganizationID: w.OrganizationID,
		Title:          title,
		Description:    description,
		Why:            why,
		Priority:       "high",
		Status:         "draft",
		CreatedBy:      workclient.SystemUserID,
	}
	if w.SpecID != nil {
		req.ParentSpecID = w.SpecID
	}

	_, err = s.workClient.CreateSpec(ctx, req)
	if err != nil {
		logger.Error("spec-creator: failed to create spec for SLO exhaustion on %s: %v", w.Name, err)
	}
}
