package http

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

func (s *Server) getCostReport(w http.ResponseWriter, r *http.Request) {
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

	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "organization"
	}
	scopeID := r.URL.Query().Get("scope_id")
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "mtd"
	}

	report, err := s.costAnalyzer.GetCostReport(r.Context(), orgID, scope, scopeID, window)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, report)
}

type budgetInput struct {
	Scope     string  `json:"scope"`
	ScopeID   string  `json:"scope_id"`
	OrgID     string  `json:"organization_id"`
	BudgetUSD float64 `json:"budget_usd"`
	AlertPcts string  `json:"alert_pcts"`
}

func (s *Server) setCostBudget(w http.ResponseWriter, r *http.Request) {
	var input budgetInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	orgID, err := uuid.Parse(input.OrgID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid organization_id")
		return
	}

	if err := s.costAnalyzer.SetBudget(r.Context(), orgID, input.Scope, input.ScopeID, input.BudgetUSD, input.AlertPcts); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Budget set",
	})
}

func (s *Server) getIdleResources(w http.ResponseWriter, r *http.Request) {
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

	resourceType := r.URL.Query().Get("resource_type")
	minIdleDays := 7 // default

	resources, err := s.costAnalyzer.GetIdleResources(r.Context(), orgID, resourceType, minIdleDays)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, resources)
}
