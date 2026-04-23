package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
	"github.com/sentiae/infrastructure-intelligence-service/internal/usecase"
)

func (s *Server) listWorkloads(w http.ResponseWriter, r *http.Request) {
	orgIDStr := r.URL.Query().Get("organization_id")
	if orgIDStr == "" {
		respondWithError(w, http.StatusBadRequest, "organization_id is required")
		return
	}
	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid organization_id")
		return
	}

	env := r.URL.Query().Get("environment")
	group := r.URL.Query().Get("group")

	var featureID *uuid.UUID
	if fid := r.URL.Query().Get("feature_id"); fid != "" {
		parsed, err := uuid.Parse(fid)
		if err == nil {
			featureID = &parsed
		}
	}

	workloads, err := s.workloadService.List(r.Context(), orgID, env, group, featureID)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, workloads)
}

func (s *Server) getWorkload(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	workload, err := s.workloadService.Get(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, workload)
}

func (s *Server) registerWorkload(w http.ResponseWriter, r *http.Request) {
	var input usecase.RegisterInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	workload, err := s.workloadService.Register(r.Context(), input)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusCreated, workload)
}

func (s *Server) getWorkloadMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	window := r.URL.Query().Get("window")
	if window == "" {
		window = "1h"
	}

	metrics, err := s.workloadService.GetMetrics(r.Context(), id, window)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, metrics)
}

func (s *Server) ingestMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	var snapshot domain.WorkloadMetricsSnapshot
	if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.workloadService.UpdateMetrics(r.Context(), id, &snapshot); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

type scaleInput struct {
	TargetReplicas int    `json:"target_replicas"`
	Reason         string `json:"reason"`
}

func (s *Server) scaleWorkload(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	var input scaleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Manual scale through workload service
	if err := s.workloadService.SetScalingBounds(r.Context(), id, &input.TargetReplicas, nil); err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"action_id":   uuid.New().String(),
		"description": "Manual scale initiated",
	})
}

type pauseInput struct {
	DurationMinutes int    `json:"duration_minutes"`
	Reason          string `json:"reason"`
}

func (s *Server) pauseAutoscaling(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	var input pauseInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.workloadService.PauseAutoscaling(r.Context(), id, input.DurationMinutes, input.Reason); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Autoscaling paused",
	})
}

func (s *Server) resumeAutoscaling(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	if err := s.workloadService.ResumeAutoscaling(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Autoscaling resumed",
	})
}

type optModeInput struct {
	Mode string `json:"mode"`
}

func (s *Server) setOptimizationMode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	var input optModeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	mode := domain.OptimizationMode(input.Mode)
	if err := s.workloadService.SetOptimizationMode(r.Context(), id, mode); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Optimization mode updated",
	})
}

type scalingBoundsInput struct {
	MinReplicas *int `json:"min_replicas,omitempty"`
	MaxReplicas *int `json:"max_replicas,omitempty"`
}

func (s *Server) setScalingBounds(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	var input scalingBoundsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.workloadService.SetScalingBounds(r.Context(), id, input.MinReplicas, input.MaxReplicas); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Scaling bounds updated",
	})
}

func (s *Server) diagnoseWorkload(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	// Get workload info
	workload, err := s.workloadService.Get(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}

	// Get SLO status
	sloStatus, _ := s.sloEngine.GetSLOStatus(r.Context(), id)

	// Get recent decisions
	decisions, _ := s.decisionEngine.GetRecentDecisions(r.Context(), id, 10)

	// Run anomaly detection
	anomaly, _ := s.anomalyDetector.Detect(r.Context(), id)

	report := map[string]interface{}{
		"workload":         workload,
		"slo_status":       sloStatus,
		"recent_decisions": decisions,
		"anomaly":          anomaly,
		"generated_at":     "now",
	}

	respondWithJSON(w, http.StatusOK, report)
}
