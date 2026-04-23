package domain

import (
	"time"

	"github.com/google/uuid"
)

// CostBudget represents a cost budget at any scope
type CostBudget struct {
	ID             uuid.UUID `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OrganizationID uuid.UUID `json:"organization_id" gorm:"type:uuid;not null;index"`
	Scope          string    `json:"scope" gorm:"type:varchar(20);not null"`   // organization, group, workload
	ScopeID        string    `json:"scope_id" gorm:"not null"`                // org ID, group name, workload ID
	BudgetUSD      float64   `json:"budget_usd" gorm:"not null"`
	AlertPcts      string    `json:"alert_pcts" gorm:"type:varchar(100)"` // comma-separated: "80,90,100"
	CurrentSpendUSD float64  `json:"current_spend_usd" gorm:"default:0"`
	Enabled        bool      `json:"enabled" gorm:"default:true"`
	CreatedAt      time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (CostBudget) TableName() string {
	return "augur_cost_budgets"
}

// CostReport represents a cost attribution report
type CostReport struct {
	Scope               string               `json:"scope"`
	Window              string               `json:"window"`
	TotalUSD            float64              `json:"total_usd"`
	ProjectedMonthlyUSD float64              `json:"projected_monthly_usd"`
	BudgetUSD           float64              `json:"budget_usd,omitempty"`
	BudgetUsedPct       float64              `json:"budget_used_pct,omitempty"`
	Breakdown           []CostBreakdown      `json:"breakdown"`
	SavingsOpportunities []SavingsOpportunity `json:"savings_opportunities"`
}

// CostBreakdown represents cost for a single workload/group
type CostBreakdown struct {
	WorkloadID       string   `json:"workload_id"`
	WorkloadName     string   `json:"workload_name"`
	FeatureID        string   `json:"feature_id,omitempty"`
	FeatureName      string   `json:"feature_name,omitempty"`
	CostUSD          float64  `json:"cost_usd"`
	CostPct          float64  `json:"cost_pct"`
	WastedUSD        float64  `json:"wasted_usd"`
	OptimizationMode string   `json:"optimization_mode"`
}

// SavingsOpportunityType represents the type of cost saving
type SavingsOpportunityType string

const (
	SavingsTypeRightsizing       SavingsOpportunityType = "rightsizing"
	SavingsTypeSpot              SavingsOpportunityType = "spot"
	SavingsTypeIdleRemoval       SavingsOpportunityType = "idle_removal"
	SavingsTypeReservedInstance  SavingsOpportunityType = "reserved_instance"
)

// SavingsOpportunity represents a potential cost saving
type SavingsOpportunity struct {
	Type                      SavingsOpportunityType `json:"type"`
	Description               string                 `json:"description"`
	EstimatedMonthlySavingsUSD float64               `json:"estimated_monthly_savings_usd"`
	Effort                    string                 `json:"effort"` // auto, low, medium, high
	WorkloadIDs               []string               `json:"workload_ids"`
}

// IdleResource represents an idle resource detected by the cost analyzer
type IdleResource struct {
	ID                      uuid.UUID `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OrganizationID          uuid.UUID `json:"organization_id" gorm:"type:uuid;not null;index"`
	ResourceID              string    `json:"resource_id" gorm:"not null"`
	ResourceType            string    `json:"resource_type" gorm:"type:varchar(30);not null"` // compute, storage, namespace
	Name                    string    `json:"name" gorm:"not null"`
	Environment             string    `json:"environment" gorm:"not null"`
	IdleSinceDays           int       `json:"idle_since_days" gorm:"default:0"`
	EstimatedMonthlyWasteUSD float64  `json:"estimated_monthly_waste_usd" gorm:"default:0"`
	LastActivityAt          *time.Time `json:"last_activity_at,omitempty"`
	Decommissioned          bool      `json:"decommissioned" gorm:"default:false"`
	CreatedAt               time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt               time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (IdleResource) TableName() string {
	return "augur_idle_resources"
}
