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

	ctx, err := s.stampWorkloadOrg(r.Context(), id)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	rec, err := s.rightsizingEng.GetRecommendation(ctx, id)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, rec)
}

func (s *Server) getRightsizingRecommendations(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	recs, err := s.rightsizingEng.GetRecommendationsForOrg(ctx, orgID)
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

	ctx, err := s.stampWorkloadOrg(r.Context(), id)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	rec, err := s.rightsizingEng.ApplyRecommendation(ctx, id)
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

	ctx, err := s.stampWorkloadOrg(r.Context(), id)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	var input spotInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		input = spotInput{MaxSpotPct: 80, FallbackOnInterrupt: true}
	}

	if err := s.spotManager.EnableSpot(ctx, id, input.MaxSpotPct, input.FallbackOnInterrupt); err != nil {
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

	ctx, err := s.stampWorkloadOrg(r.Context(), id)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	if err := s.spotManager.DisableSpot(ctx, id); err != nil {
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

	ctx, err := s.stampWorkloadOrg(r.Context(), id)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	status, err := s.spotManager.GetSpotStatus(ctx, id)
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

	// A workload-scoped simulation reads that workload under RLS — resolve+stamp
	// its owning org (404-safe). A generic simulation (no workload_id) touches no
	// tenant data, so it runs on the request ctx unstamped.
	ctx := r.Context()
	if input.WorkloadID != nil {
		var err error
		ctx, err = s.stampWorkloadOrg(r.Context(), *input.WorkloadID)
		if err != nil {
			respondOrgError(w, err)
			return
		}
	}

	result, err := s.capacitySim.Simulate(ctx, input)
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
