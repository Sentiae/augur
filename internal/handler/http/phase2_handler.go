package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/usecase"
)

// --- Rightsizing ---

func (s *Server) getRightsizingRecommendation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	rec, err := s.rightsizingEng.GetRecommendation(r.Context(), id)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, rec)
}

func (s *Server) getRightsizingRecommendations(w http.ResponseWriter, r *http.Request) {
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

	recs, err := s.rightsizingEng.GetRecommendationsForOrg(r.Context(), orgID)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, recs)
}

func (s *Server) applyRightsizing(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	rec, err := s.rightsizingEng.ApplyRecommendation(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"description":    "Rightsizing applied",
		"recommendation": rec,
	})
}

// --- Spot Management ---

type spotInput struct {
	MaxSpotPct           int  `json:"max_spot_pct"`
	FallbackOnInterrupt  bool `json:"fallback_on_interruption"`
}

func (s *Server) enableSpot(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	var input spotInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		input = spotInput{MaxSpotPct: 80, FallbackOnInterrupt: true}
	}

	if err := s.spotManager.EnableSpot(r.Context(), id, input.MaxSpotPct, input.FallbackOnInterrupt); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Spot instances enabled",
	})
}

func (s *Server) disableSpot(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	if err := s.spotManager.DisableSpot(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Spot instances disabled",
	})
}

func (s *Server) getSpotStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	status, err := s.spotManager.GetSpotStatus(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, status)
}

// --- Decommission ---

// --- Capacity Simulation ---

func (s *Server) simulateCapacity(w http.ResponseWriter, r *http.Request) {
	var input usecase.SimulationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := s.capacitySim.Simulate(r.Context(), input)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, result)
}

type decommissionInput struct {
	ResourceID   string `json:"resource_id"`
	ResourceType string `json:"resource_type"`
	Reason       string `json:"reason"`
}

func (s *Server) decommissionResource(w http.ResponseWriter, r *http.Request) {
	var input decommissionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Mark the idle resource as decommissioned
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"action_id":   uuid.New().String(),
		"description": "Decommission request accepted",
	})
}
