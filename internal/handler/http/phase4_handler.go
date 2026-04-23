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

	score, err := s.multiLayerDetector.Detect(r.Context(), id)
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

	if err := s.multiLayerDetector.TrainModels(r.Context(), id); err != nil {
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

	windowDays := 30
	if w := r.URL.Query().Get("window_days"); w != "" {
		if d, err := strconv.Atoi(w); err == nil && d > 0 {
			windowDays = d
		}
	}

	portfolio, err := s.riRecommender.Recommend(r.Context(), orgID, windowDays)
	if err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, portfolio)
}

// --- Cross-Cluster Optimization ---

func (s *Server) getPlacementRecommendation(w http.ResponseWriter, r *http.Request) {
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

	recs, err := s.crossClusterOpt.AnalyzePlacement(r.Context(), orgID)
	if err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, recs)
}

func (s *Server) getInstanceTypeRecommendation(w http.ResponseWriter, r *http.Request) {
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

	recs, err := s.crossClusterOpt.RecommendInstanceTypes(r.Context(), orgID)
	if err != nil {
		handleError(w, err)
		return
	}

	respondWithJSON(w, http.StatusOK, recs)
}
