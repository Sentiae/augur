package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- Multi-Layer Anomaly Detection ---

func (s *Server) detectAnomalyMultiLayer(w http.ResponseWriter, r *http.Request) {
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

	score, err := s.multiLayerDetector.Detect(ctx, id)
	if err != nil {
		handleError(w, err)
		return
	}

	if score == nil {
		respondWithJSON(w, http.StatusOK, map[string]interface{}{
			"anomaly_detected": false,
		})
		return
	}

	respondWithJSON(w, http.StatusOK, score)
}

func (s *Server) trainAnomalyModels(w http.ResponseWriter, r *http.Request) {
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

	if err := s.multiLayerDetector.TrainModels(ctx, id); err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"description": "Anomaly detection models trained",
	})
}

// --- RI / Savings Plan Recommendations ---

func (s *Server) getRIRecommendation(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	windowDays := 30
	if w := r.URL.Query().Get("window_days"); w != "" {
		if d, err := strconv.Atoi(w); err == nil && d > 0 {
			windowDays = d
		}
	}

	portfolio, err := s.riRecommender.Recommend(ctx, orgID, windowDays)
	if err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, portfolio)
}

// --- Cross-Cluster Optimization ---

func (s *Server) getPlacementRecommendation(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	recs, err := s.crossClusterOpt.AnalyzePlacement(ctx, orgID)
	if err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, recs)
}

func (s *Server) getInstanceTypeRecommendation(w http.ResponseWriter, r *http.Request) {
	orgID, ctx, err := orgFromRequest(r)
	if err != nil {
		respondOrgError(w, err)
		return
	}

	recs, err := s.crossClusterOpt.RecommendInstanceTypes(ctx, orgID)
	if err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, recs)
}
