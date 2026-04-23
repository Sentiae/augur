package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sentiae/infrastructure-intelligence-service/internal/domain"
)

type apiResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func respondWithJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiResponse{Success: status < 400, Data: data})
}

func respondWithError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiResponse{Success: false, Error: message})
}

func handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrWorkloadNotFound),
		errors.Is(err, domain.ErrDecisionNotFound),
		errors.Is(err, domain.ErrPolicyNotFound),
		errors.Is(err, domain.ErrSLONotFound),
		errors.Is(err, domain.ErrBudgetNotFound),
		errors.Is(err, domain.ErrNotFound):
		respondWithError(w, http.StatusNotFound, err.Error())

	case errors.Is(err, domain.ErrWorkloadAlreadyExists),
		errors.Is(err, domain.ErrSLOAlreadyExists),
		errors.Is(err, domain.ErrPolicyConflict):
		respondWithError(w, http.StatusConflict, err.Error())

	case errors.Is(err, domain.ErrInvalidInput),
		errors.Is(err, domain.ErrInvalidPolicyScope),
		errors.Is(err, domain.ErrScaleBoundsExceeded):
		respondWithError(w, http.StatusBadRequest, err.Error())

	case errors.Is(err, domain.ErrCooldownActive),
		errors.Is(err, domain.ErrRateLimitExceeded):
		respondWithError(w, http.StatusTooManyRequests, err.Error())

	case errors.Is(err, domain.ErrApprovalRequired):
		respondWithError(w, http.StatusForbidden, err.Error())

	case errors.Is(err, domain.ErrWorkloadPaused),
		errors.Is(err, domain.ErrWorkloadObserving),
		errors.Is(err, domain.ErrWorkloadCircuitOpen):
		respondWithError(w, http.StatusConflict, err.Error())

	case errors.Is(err, domain.ErrUnauthorized):
		respondWithError(w, http.StatusUnauthorized, err.Error())

	default:
		respondWithError(w, http.StatusInternalServerError, "internal server error")
	}
}
