package http

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

func (s *Server) getCostReport(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
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

	report, err := s.costAnalyzer.GetCostReport(ctx, orgID, scope, scopeID, window)
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

	ctx, err := authorizeAndStampOrg(r.Context(), orgID)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	if err := s.costAnalyzer.SetBudget(ctx, orgID, input.Scope, input.ScopeID, input.BudgetUSD, input.AlertPcts); err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Budget set",
	})
}

func (s *Server) getIdleResources(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	resourceType := r.URL.Query().Get("resource_type")
	minIdleDays := 7 // default

	resources, err := s.costAnalyzer.GetIdleResources(ctx, orgID, resourceType, minIdleDays)
	if err != nil {
		handleError(w, err)
		return
	}
	respondWithJSON(w, http.StatusOK, resources)
}
