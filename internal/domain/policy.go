package domain

import (
	"time"

	"github.com/google/uuid"
)

// PolicyScope represents the scope level of a policy
type PolicyScope string

const (
	PolicyScopeGlobal PolicyScope = "global"
	PolicyScopeGroup  PolicyScope = "group"
	PolicyScopeApp    PolicyScope = "app"
)

// AugurPolicy represents an infrastructure policy at any level in the hierarchy
type AugurPolicy struct {
	ID               uuid.UUID        `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OrganizationID   uuid.UUID        `json:"organization_id" gorm:"type:uuid;not null;index"`
	Scope            PolicyScope      `json:"scope" gorm:"type:varchar(10);not null"`
	ScopeID          string           `json:"scope_id" gorm:"not null"` // org ID for global, group name for group, workload ID for app
	Name             string           `json:"name" gorm:"not null"`
	Description      string           `json:"description,omitempty" gorm:"type:text"`
	OptimizationMode *OptimizationMode `json:"optimization_mode,omitempty" gorm:"type:varchar(20)"`
	MinReplicas      *int             `json:"min_replicas,omitempty"`
	MaxReplicas      *int             `json:"max_replicas,omitempty"`
	MaxBudgetUSD     *float64         `json:"max_budget_usd,omitempty"`
	EnableSpot       *bool            `json:"enable_spot,omitempty"`
	ScalingRules     string           `json:"scaling_rules,omitempty" gorm:"type:text"` // JSON-encoded CEL rules
	Enabled          bool             `json:"enabled" gorm:"default:true"`
	Priority         int              `json:"priority" gorm:"default:0"` // higher = more important within same scope

	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (AugurPolicy) TableName() string {
	return "augur_policies"
}

// ResolvedPolicy represents the final policy after hierarchy resolution
type ResolvedPolicy struct {
	OptimizationMode OptimizationMode
	MinReplicas      int
	MaxReplicas      int
	MaxBudgetUSD     float64
	EnableSpot       bool
	Weights          PolicyWeights
	ScalingRules     []ScalingRule
}

// ScalingRule represents a CEL-based scaling rule
type ScalingRule struct {
	Name              string `json:"name"`
	Condition         string `json:"condition"` // CEL expression
	Action            string `json:"action"`    // scale_up, scale_down, set_min_replicas
	TargetReplicasPct int    `json:"target_replicas_pct,omitempty"`
	Value             int    `json:"value,omitempty"`
	RequiresApproval  bool   `json:"requires_approval,omitempty"`
}

// ResolvePolicy merges policies from all hierarchy levels following conflict resolution rules
func ResolvePolicy(global, group, app *AugurPolicy) ResolvedPolicy {
	resolved := ResolvedPolicy{
		OptimizationMode: OptimizationModeBalanced,
		MinReplicas:      1,
		MaxReplicas:      100,
		MaxBudgetUSD:     0, // 0 = unlimited
		EnableSpot:       false,
	}

	// Apply global defaults
	if global != nil {
		applyPolicy(&resolved, global)
	}

	// Apply group overrides
	if group != nil {
		applyPolicy(&resolved, group)
	}

	// Apply app-level overrides
	if app != nil {
		applyPolicy(&resolved, app)
	}

	// Set weights based on final optimization mode
	resolved.Weights = WeightsForMode(resolved.OptimizationMode)

	return resolved
}

func applyPolicy(resolved *ResolvedPolicy, p *AugurPolicy) {
	// Optimization preferences: child overrides parent
	if p.OptimizationMode != nil {
		resolved.OptimizationMode = *p.OptimizationMode
	}
	if p.EnableSpot != nil {
		resolved.EnableSpot = *p.EnableSpot
	}

	// Safety constraints: most-restrictive wins
	if p.MinReplicas != nil && *p.MinReplicas > resolved.MinReplicas {
		resolved.MinReplicas = *p.MinReplicas
	}
	if p.MaxReplicas != nil && *p.MaxReplicas < resolved.MaxReplicas {
		resolved.MaxReplicas = *p.MaxReplicas
	}
	if p.MaxBudgetUSD != nil {
		if resolved.MaxBudgetUSD == 0 || *p.MaxBudgetUSD < resolved.MaxBudgetUSD {
			resolved.MaxBudgetUSD = *p.MaxBudgetUSD
		}
	}
}
