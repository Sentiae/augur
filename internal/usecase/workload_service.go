package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/repository/postgres"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/events"
	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// WorkloadService manages workload lifecycle and queries
type WorkloadService struct {
	workloadRepo *postgres.WorkloadRepository
	metricsRepo  *postgres.MetricsRepository
	policyRepo   *postgres.PolicyRepository
	publisher    events.EventPublisher
	observeDays  int
}

func NewWorkloadService(
	workloadRepo *postgres.WorkloadRepository,
	metricsRepo *postgres.MetricsRepository,
	policyRepo *postgres.PolicyRepository,
	publisher events.EventPublisher,
	observeDays int,
) *WorkloadService {
	return &WorkloadService{
		workloadRepo: workloadRepo,
		metricsRepo:  metricsRepo,
		policyRepo:   policyRepo,
		publisher:    publisher,
		observeDays:  observeDays,
	}
}

// RegisterInput is the input for registering a new workload
type RegisterInput struct {
	OrganizationID   uuid.UUID              `json:"organization_id"`
	WorkloadName     string                 `json:"workload_name"`
	WorkloadType     domain.WorkloadType    `json:"workload_type"`
	EnvironmentID    string                 `json:"environment_id"`
	FeatureID        *uuid.UUID             `json:"feature_id,omitempty"`
	SpecID           *uuid.UUID             `json:"spec_id,omitempty"`
	OptimizationMode domain.OptimizationMode `json:"optimization_mode,omitempty"`
}

// Register adds a new workload to Augur management
func (s *WorkloadService) Register(ctx context.Context, input RegisterInput) (*domain.Workload, error) {
	mode := domain.OptimizationModeBalanced
	if input.OptimizationMode != "" {
		mode = input.OptimizationMode
	}

	observeUntil := time.Now().AddDate(0, 0, s.observeDays)

	w := &domain.Workload{
		ID:                 uuid.New(),
		OrganizationID:     input.OrganizationID,
		Name:               input.WorkloadName,
		WorkloadType:       input.WorkloadType,
		Environment:        input.EnvironmentID,
		FeatureID:          input.FeatureID,
		SpecID:             input.SpecID,
		CurrentReplicas:    1,
		DesiredReplicas:    1,
		MinReplicas:        1,
		MaxReplicas:        10,
		OptimizationMode:   mode,
		Status:             domain.WorkloadStatusObserving,
		ObserveMode:        true,
		ObserveUntil:       &observeUntil,
		AutoscalingEnabled: true,
		SLOCompliancePct:   100,
	}

	if err := s.workloadRepo.Create(ctx, w); err != nil {
		return nil, err
	}

	_ = s.publisher.Publish(ctx, events.EventWorkloadRegistered, events.EventData{
		ResourceID:   w.ID.String(),
		ResourceType: "workload",
		Metadata: map[string]any{
			"workload_name": w.Name,
			"workload_type": string(w.WorkloadType),
			"environment":   w.Environment,
		},
	})

	logger.Info("Workload registered: id=%s, name=%s, type=%s", w.ID, w.Name, w.WorkloadType)
	return w, nil
}

// List returns workloads for an organization with optional filters
func (s *WorkloadService) List(ctx context.Context, orgID uuid.UUID, env, group string, featureID *uuid.UUID) ([]*domain.Workload, error) {
	return s.workloadRepo.FindByOrganizationFiltered(ctx, orgID, env, group, featureID)
}

// Get returns a single workload by ID
func (s *WorkloadService) Get(ctx context.Context, id uuid.UUID) (*domain.Workload, error) {
	return s.workloadRepo.FindByID(ctx, id)
}

// GetMetrics returns metrics history for a workload
func (s *WorkloadService) GetMetrics(ctx context.Context, workloadID uuid.UUID, window string) ([]*domain.WorkloadMetricsSnapshot, error) {
	since := parseWindow(window)
	return s.metricsRepo.FindByWorkload(ctx, workloadID, since, 1000)
}

// UpdateMetrics stores a new metrics snapshot and updates the workload's current metrics
func (s *WorkloadService) UpdateMetrics(ctx context.Context, workloadID uuid.UUID, snapshot *domain.WorkloadMetricsSnapshot) error {
	snapshot.WorkloadID = workloadID
	snapshot.ID = uuid.New()
	if snapshot.Timestamp.IsZero() {
		snapshot.Timestamp = time.Now()
	}

	if err := s.metricsRepo.Create(ctx, snapshot); err != nil {
		return err
	}

	// Update workload's current metrics
	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	now := time.Now()
	w.CPUUtilizationPct = snapshot.CPUPct
	w.MemoryUtilizationPct = snapshot.MemoryPct
	w.RequestsPerSec = snapshot.RequestsPerSec
	w.LatencyP99Ms = snapshot.LatencyP99Ms
	w.ErrorRatePct = snapshot.ErrorRatePct
	w.LastMetricsAt = &now

	return s.workloadRepo.Update(ctx, w)
}

// CheckObservationPeriod checks if any workloads should exit observation mode
func (s *WorkloadService) CheckObservationPeriod(ctx context.Context) error {
	workloads, err := s.workloadRepo.FindAllManaged(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, w := range workloads {
		if w.ObserveMode && w.ObserveUntil != nil && now.After(*w.ObserveUntil) {
			w.ObserveMode = false
			w.Status = domain.WorkloadStatusHealthy
			if err := s.workloadRepo.Update(ctx, w); err != nil {
				logger.Error("Failed to exit observe mode for workload %s: %v", w.ID, err)
				continue
			}

			_ = s.publisher.Publish(ctx, events.EventWorkloadObserved, events.EventData{
				ResourceID:   w.ID.String(),
				ResourceType: "workload",
				Metadata: map[string]any{
					"workload_name": w.Name,
				},
			})

			logger.Info("Workload exited observation mode: id=%s, name=%s", w.ID, w.Name)
		}
	}
	return nil
}

// PauseAutoscaling pauses autoscaling for a workload
func (s *WorkloadService) PauseAutoscaling(ctx context.Context, workloadID uuid.UUID, durationMin int, reason string) error {
	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	until := time.Now().Add(time.Duration(durationMin) * time.Minute)
	w.AutoscalingPaused = true
	w.PausedUntil = &until
	w.PauseReason = reason
	w.Status = domain.WorkloadStatusPaused

	return s.workloadRepo.Update(ctx, w)
}

// ResumeAutoscaling resumes autoscaling for a workload
func (s *WorkloadService) ResumeAutoscaling(ctx context.Context, workloadID uuid.UUID) error {
	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	w.AutoscalingPaused = false
	w.PausedUntil = nil
	w.PauseReason = ""
	w.Status = domain.WorkloadStatusHealthy

	return s.workloadRepo.Update(ctx, w)
}

// SetOptimizationMode changes the optimization mode for a workload
func (s *WorkloadService) SetOptimizationMode(ctx context.Context, workloadID uuid.UUID, mode domain.OptimizationMode) error {
	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	w.OptimizationMode = mode
	return s.workloadRepo.Update(ctx, w)
}

// SetScalingBounds sets min/max replicas for a workload
func (s *WorkloadService) SetScalingBounds(ctx context.Context, workloadID uuid.UUID, minReplicas, maxReplicas *int) error {
	w, err := s.workloadRepo.FindByID(ctx, workloadID)
	if err != nil {
		return err
	}

	if minReplicas != nil {
		w.MinReplicas = *minReplicas
	}
	if maxReplicas != nil {
		w.MaxReplicas = *maxReplicas
	}
	return s.workloadRepo.Update(ctx, w)
}

func parseWindow(window string) time.Time {
	switch window {
	case "1h":
		return time.Now().Add(-1 * time.Hour)
	case "6h":
		return time.Now().Add(-6 * time.Hour)
	case "1d", "24h":
		return time.Now().Add(-24 * time.Hour)
	case "7d":
		return time.Now().Add(-7 * 24 * time.Hour)
	case "30d":
		return time.Now().Add(-30 * 24 * time.Hour)
	default:
		return time.Now().Add(-1 * time.Hour)
	}
}
