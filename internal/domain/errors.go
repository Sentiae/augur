package domain

import "errors"

var (
	// Workload errors
	ErrWorkloadNotFound      = errors.New("workload not found")
	ErrWorkloadAlreadyExists = errors.New("workload already exists")
	ErrWorkloadPaused        = errors.New("autoscaling is paused for this workload")
	ErrWorkloadObserving     = errors.New("workload is in observation mode")
	ErrWorkloadCircuitOpen   = errors.New("circuit breaker is open for this workload")

	// Policy errors
	ErrPolicyNotFound     = errors.New("policy not found")
	ErrInvalidPolicyScope = errors.New("invalid policy scope")
	ErrPolicyConflict     = errors.New("policy conflict detected")

	// Decision errors
	ErrDecisionNotFound  = errors.New("decision not found")
	ErrCooldownActive    = errors.New("scaling cooldown is active")
	ErrRateLimitExceeded = errors.New("scaling rate limit exceeded")
	ErrScaleBoundsExceeded = errors.New("target replicas exceeds policy bounds")

	// SLO errors
	ErrSLONotFound       = errors.New("SLO definition not found")
	ErrSLOAlreadyExists  = errors.New("SLO definition already exists for this type")

	// Cost errors
	ErrBudgetNotFound    = errors.New("cost budget not found")
	ErrBudgetExceeded    = errors.New("cost budget exceeded")

	// Safety errors
	ErrApprovalRequired    = errors.New("this action requires approval")
	ErrRollbackUnavailable = errors.New("no rollback available for this action")

	// General
	ErrNotFound          = errors.New("not found")
	ErrInvalidInput      = errors.New("invalid input")
	ErrUnauthorized      = errors.New("unauthorized")
)
