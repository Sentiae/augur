package domain

import (
	"time"

	"github.com/google/uuid"
)

// SLOType represents the type of SLO being tracked
type SLOType string

const (
	SLOTypeAvailability SLOType = "availability"
	SLOTypeLatency      SLOType = "latency"
	SLOTypeErrorRate    SLOType = "error_rate"
)

// SLOMode represents the current SLO health mode
type SLOMode string

const (
	SLOModeNormal    SLOMode = "normal"
	SLOModeWarning   SLOMode = "warning"
	SLOModeCritical  SLOMode = "critical"
	SLOModeEmergency SLOMode = "emergency"
)

// SLODefinition represents an SLO target for a workload
type SLODefinition struct {
	ID             uuid.UUID `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	WorkloadID     uuid.UUID `json:"workload_id" gorm:"type:uuid;not null;index"`
	OrganizationID uuid.UUID `json:"organization_id" gorm:"type:uuid;not null;index"`
	SLOType        SLOType   `json:"slo_type" gorm:"type:varchar(20);not null"`
	TargetPct      float64   `json:"target_pct" gorm:"not null"`    // e.g., 99.9
	WindowDays     int       `json:"window_days" gorm:"default:30"` // rolling window
	Enabled        bool      `json:"enabled" gorm:"default:true"`
	CreatedAt      time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (SLODefinition) TableName() string {
	return "augur_slo_definitions"
}

// SLOStatus represents the computed SLO status for a workload
type SLOStatus struct {
	WorkloadID              uuid.UUID `json:"workload_id"`
	SLOTargetPct            float64   `json:"slo_target_pct"`
	CurrentCompliancePct    float64   `json:"current_compliance_pct"`
	ErrorBudgetRemainingPct float64   `json:"error_budget_remaining_pct"`
	BurnRates               BurnRates `json:"burn_rates"`
	Mode                    SLOMode   `json:"mode"`
	FreezeRecommended       bool      `json:"freeze_recommended"`
}

// BurnRates represents the burn rate across multiple windows
type BurnRates struct {
	Window1h float64 `json:"window_1h"`
	Window6h float64 `json:"window_6h"`
	Window1d float64 `json:"window_1d"`
	Window3d float64 `json:"window_3d"`
}

// ComputeSLOMode determines the SLO mode from burn rates and error budget
func ComputeSLOMode(budgetRemainingPct float64, burnRates BurnRates) SLOMode {
	if budgetRemainingPct <= 5 {
		return SLOModeEmergency
	}
	if budgetRemainingPct <= 20 {
		return SLOModeCritical
	}
	if burnRates.Window1h > 14.4 || burnRates.Window6h > 6 {
		return SLOModeCritical
	}
	if burnRates.Window1d > 3 || burnRates.Window3d > 1 {
		return SLOModeWarning
	}
	return SLOModeNormal
}

// SLOBurnRateLog stores computed burn rate values over time
type SLOBurnRateLog struct {
	ID                     uuid.UUID `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	WorkloadID             uuid.UUID `json:"workload_id" gorm:"type:uuid;not null;index:idx_burnrate_workload_ts"`
	SLODefinitionID        uuid.UUID `json:"slo_definition_id" gorm:"type:uuid;not null"`
	BurnRate1h             float64   `json:"burn_rate_1h"`
	BurnRate6h             float64   `json:"burn_rate_6h"`
	BurnRate1d             float64   `json:"burn_rate_1d"`
	BurnRate3d             float64   `json:"burn_rate_3d"`
	ErrorBudgetRemainingPct float64  `json:"error_budget_remaining_pct"`
	Mode                   SLOMode   `json:"mode" gorm:"type:varchar(20)"`
	Timestamp              time.Time `json:"timestamp" gorm:"not null;index:idx_burnrate_workload_ts"`
}

func (SLOBurnRateLog) TableName() string {
	return "augur_slo_burn_rate_log"
}
