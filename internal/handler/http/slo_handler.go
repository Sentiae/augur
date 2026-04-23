package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

func (s *Server) getSLOStatus(w http.ResponseWriter, r *http.Request) {
	workloadID, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	status, err := s.sloEngine.GetSLOStatus(r.Context(), workloadID)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, status)
}

type createSLOInput struct {
	WorkloadID     string  `json:"workload_id"`
	OrganizationID string  `json:"organization_id"`
	SLOType        string  `json:"slo_type"`
	TargetPct      float64 `json:"target_pct"`
	WindowDays     int     `json:"window_days"`
}

func (s *Server) createSLO(w http.ResponseWriter, r *http.Request) {
	var input createSLOInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	workloadID, err := uuid.Parse(input.WorkloadID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload_id")
		return
	}
	orgID, err := uuid.Parse(input.OrganizationID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid organization_id")
		return
	}

	windowDays := input.WindowDays
	if windowDays == 0 {
		windowDays = 30
	}

	def := &domain.SLODefinition{
		WorkloadID:     workloadID,
		OrganizationID: orgID,
		SLOType:        domain.SLOType(input.SLOType),
		TargetPct:      input.TargetPct,
		WindowDays:     windowDays,
		Enabled:        true,
	}

	if err := s.sloEngine.CreateSLO(r.Context(), def); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusCreated, def)
}
