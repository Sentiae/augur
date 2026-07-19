package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// StringSlice is a []string persisted as a JSONB column. It implements
// driver.Valuer/sql.Scanner (marshalling to/from JSON) so agent workload
// bindings can be stored inline without a join table or a new dependency.
type StringSlice []string

// Value marshals the slice to JSON for storage. A nil slice serializes as an
// empty JSON array so the column is never SQL NULL.
func (s StringSlice) Value() (driver.Value, error) {
	if s == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(s))
}

// Scan unmarshals a JSONB column back into the slice. It accepts nil (empty
// slice), []byte, and string inputs.
func (s *StringSlice) Scan(src any) error {
	if src == nil {
		*s = StringSlice{}
		return nil
	}
	var data []byte
	switch v := src.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("StringSlice: unsupported scan type %T", src)
	}
	if len(data) == 0 {
		*s = StringSlice{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("StringSlice: unmarshal: %w", err)
	}
	*s = out
	return nil
}

// AgentStatus is the lifecycle state of an enrolled agent.
type AgentStatus string

const (
	AgentStatusPending AgentStatus = "pending"
	AgentStatusActive  AgentStatus = "active"
	AgentStatusRevoked AgentStatus = "revoked"
)

// Valid reports whether the status is one of the known values.
func (s AgentStatus) Valid() bool {
	switch s {
	case AgentStatusPending, AgentStatusActive, AgentStatusRevoked:
		return true
	default:
		return false
	}
}

// Agent is an enrolled agent-plane identity (a probe/deploy/telemetry agent
// bound to an organization and a set of workloads).
type Agent struct {
	ID               uuid.UUID   `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OrganizationID   uuid.UUID   `json:"organization_id" gorm:"type:uuid;not null;index"`
	AgentType        string      `json:"agent_type" gorm:"not null"`
	Hostname         string      `json:"hostname"`
	Status           AgentStatus `json:"status" gorm:"type:varchar(20);not null;default:'pending'"`
	CertFingerprint  string      `json:"cert_fingerprint" gorm:"column:cert_fingerprint"` // SHA-256 hex of agent cert pubkey; empty until enrolled
	WorkloadBindings StringSlice `json:"workload_bindings" gorm:"type:jsonb"`
	LastSeenAt       *time.Time  `json:"last_seen_at,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

func (Agent) TableName() string {
	return "augur_agents"
}

// NewAgent constructs a pending agent. It requires a non-nil org and a
// non-empty agent type; the certificate fingerprint is assigned later at
// enrollment (Activate).
func NewAgent(orgID uuid.UUID, agentType, hostname string, bindings []string) (*Agent, error) {
	if orgID == uuid.Nil {
		return nil, ErrInvalidAgentOrg
	}
	if agentType == "" {
		return nil, ErrInvalidAgentType
	}
	now := time.Now().UTC()
	return &Agent{
		ID:               uuid.New(),
		OrganizationID:   orgID,
		AgentType:        agentType,
		Hostname:         hostname,
		Status:           AgentStatusPending,
		WorkloadBindings: StringSlice(bindings),
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

// Activate enrolls the agent with a certificate fingerprint. It moves a pending
// or revoked (re-enroll) agent to active. Re-activating an already-active agent
// with the SAME fingerprint is an idempotent no-op; a DIFFERENT fingerprint on
// an active agent is rejected as a mismatch.
func (a *Agent) Activate(fingerprint string) error {
	if a.Status == AgentStatusActive {
		if a.CertFingerprint == fingerprint {
			return nil
		}
		return ErrAgentFingerprintMismatch
	}
	a.CertFingerprint = fingerprint
	a.Status = AgentStatusActive
	a.UpdatedAt = time.Now().UTC()
	return nil
}

// Revoke moves the agent to revoked. It is idempotent.
func (a *Agent) Revoke() error {
	if a.Status == AgentStatusRevoked {
		return nil
	}
	a.Status = AgentStatusRevoked
	a.UpdatedAt = time.Now().UTC()
	return nil
}

// IsActive reports whether the agent is currently active.
func (a *Agent) IsActive() bool {
	return a.Status == AgentStatusActive
}

// HasWorkload reports whether the agent is bound to the given workload id.
func (a *Agent) HasWorkload(id string) bool {
	for _, w := range a.WorkloadBindings {
		if w == id {
			return true
		}
	}
	return false
}
