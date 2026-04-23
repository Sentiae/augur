package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
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

	severity := r.URL.Query().Get("severity")
	alertType := r.URL.Query().Get("type")

	alerts, err := s.alertService.ListAlerts(r.Context(), orgID, severity, alertType)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, alerts)
}

func (s *Server) explainDecision(w http.ResponseWriter, r *http.Request) {
	decisionID, err := uuid.Parse(chi.URLParam(r, "decisionID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid decision ID")
		return
	}

	decision, err := s.decisionEngine.ExplainDecision(r.Context(), decisionID)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, decision)
}

func (s *Server) listDecisions(w http.ResponseWriter, r *http.Request) {
	workloadIDStr := r.URL.Query().Get("workload_id")
	if workloadIDStr == "" {
		respondWithError(w, http.StatusBadRequest, "workload_id is required")
		return
	}
	workloadID, err := uuid.Parse(workloadIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload_id")
		return
	}

	decisions, err := s.decisionEngine.GetRecentDecisions(r.Context(), workloadID, 20)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, decisions)
}

func (s *Server) getForecast(w http.ResponseWriter, r *http.Request) {
	workloadID, err := uuid.Parse(chi.URLParam(r, "workloadID"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid workload ID")
		return
	}

	horizonHours := 6
	if h := r.URL.Query().Get("horizon_hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 && v <= 24 {
			horizonHours = v
		}
	}

	forecast, err := s.predictionEngine.GetForecast(r.Context(), workloadID, horizonHours)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, forecast)
}
