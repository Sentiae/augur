package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	severity := r.URL.Query().Get("severity")
	alertType := r.URL.Query().Get("type")

	alerts, err := s.alertService.ListAlerts(ctx, orgID, severity, alertType)
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

	ctx, err := s.stampDecisionOrg(r.Context(), decisionID)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	decision, err := s.decisionEngine.ExplainDecision(ctx, decisionID)
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

	ctx, err := s.stampWorkloadOrg(r.Context(), workloadID)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	decisions, err := s.decisionEngine.GetRecentDecisions(ctx, workloadID, 20)
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

	ctx, err := s.stampWorkloadOrg(r.Context(), workloadID)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	horizonHours := 6
	if h := r.URL.Query().Get("horizon_hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil && v > 0 && v <= 24 {
			horizonHours = v
		}
	}

	forecast, err := s.predictionEngine.GetForecast(ctx, workloadID, horizonHours)
	if err != nil {
		respondWithError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	respondWithJSON(w, http.StatusOK, forecast)
}
