package domain

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestNewAgent(t *testing.T) {
	org := uuid.New()
	tests := []struct {
		name      string
		orgID     uuid.UUID
		agentType string
		wantErr   error
	}{
		{"valid", org, "probe", nil},
		{"nil org", uuid.Nil, "probe", ErrInvalidAgentOrg},
		{"empty type", org, "", ErrInvalidAgentType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewAgent(tt.orgID, tt.agentType, "host-1", []string{"w1"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got err %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if a.ID == uuid.Nil {
				t.Error("expected minted ID")
			}
			if a.Status != AgentStatusPending {
				t.Errorf("status: got %v, want %v", a.Status, AgentStatusPending)
			}
			if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
				t.Error("expected UTC timestamps set")
			}
			if a.CertFingerprint != "" {
				t.Error("expected empty fingerprint before enrollment")
			}
		})
	}
}

func TestAgentActivate(t *testing.T) {
	newAgent := func(status AgentStatus, fp string) *Agent {
		return &Agent{Status: status, CertFingerprint: fp}
	}
	tests := []struct {
		name       string
		agent      *Agent
		fingerprint string
		wantErr    error
		wantStatus AgentStatus
		wantFP     string
	}{
		{"pending to active", newAgent(AgentStatusPending, ""), "fp-a", nil, AgentStatusActive, "fp-a"},
		{"revoked re-enroll", newAgent(AgentStatusRevoked, "old"), "fp-b", nil, AgentStatusActive, "fp-b"},
		{"active same fp noop", newAgent(AgentStatusActive, "fp-c"), "fp-c", nil, AgentStatusActive, "fp-c"},
		{"active diff fp mismatch", newAgent(AgentStatusActive, "fp-c"), "fp-d", ErrAgentFingerprintMismatch, AgentStatusActive, "fp-c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.agent.Activate(tt.fingerprint)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got err %v, want %v", err, tt.wantErr)
			}
			if tt.agent.Status != tt.wantStatus {
				t.Errorf("status: got %v, want %v", tt.agent.Status, tt.wantStatus)
			}
			if tt.agent.CertFingerprint != tt.wantFP {
				t.Errorf("fingerprint: got %q, want %q", tt.agent.CertFingerprint, tt.wantFP)
			}
		})
	}
}

func TestAgentRevoke(t *testing.T) {
	a := &Agent{Status: AgentStatusActive, CertFingerprint: "fp"}
	if err := a.Revoke(); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if a.Status != AgentStatusRevoked {
		t.Errorf("status: got %v, want revoked", a.Status)
	}
	// idempotent
	if err := a.Revoke(); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if a.IsActive() {
		t.Error("revoked agent must not be active")
	}
}

func TestAgentIsActive(t *testing.T) {
	if (&Agent{Status: AgentStatusActive}).IsActive() != true {
		t.Error("active should be active")
	}
	if (&Agent{Status: AgentStatusPending}).IsActive() != false {
		t.Error("pending should not be active")
	}
}

func TestAgentHasWorkload(t *testing.T) {
	a := &Agent{WorkloadBindings: StringSlice{"w1", "w2"}}
	if !a.HasWorkload("w1") {
		t.Error("expected w1 bound")
	}
	if a.HasWorkload("w3") {
		t.Error("did not expect w3 bound")
	}
	empty := &Agent{}
	if empty.HasWorkload("w1") {
		t.Error("empty bindings must not match")
	}
}

func TestAgentStatusValid(t *testing.T) {
	tests := []struct {
		status AgentStatus
		want   bool
	}{
		{AgentStatusPending, true},
		{AgentStatusActive, true},
		{AgentStatusRevoked, true},
		{AgentStatus("bogus"), false},
	}
	for _, tt := range tests {
		if got := tt.status.Valid(); got != tt.want {
			t.Errorf("%q.Valid() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestStringSliceValueScan(t *testing.T) {
	tests := []struct {
		name string
		in   StringSlice
		want []string
	}{
		{"non-empty", StringSlice{"a", "b"}, []string{"a", "b"}},
		{"empty", StringSlice{}, []string{}},
		{"nil", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.in.Value()
			if err != nil {
				t.Fatalf("Value: %v", err)
			}
			b, ok := v.([]byte)
			if !ok {
				t.Fatalf("Value: got %T, want []byte", v)
			}
			var got StringSlice
			if err := got.Scan(b); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if !reflect.DeepEqual([]string(got), tt.want) {
				t.Errorf("round-trip: got %v, want %v", []string(got), tt.want)
			}
		})
	}
}

func TestStringSliceScanInputs(t *testing.T) {
	// nil src -> empty slice
	var s StringSlice
	if err := s.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if len(s) != 0 {
		t.Errorf("Scan(nil): got %v, want empty", s)
	}
	// string src
	var fromStr StringSlice
	if err := fromStr.Scan(`["x","y"]`); err != nil {
		t.Fatalf("Scan(string): %v", err)
	}
	if !reflect.DeepEqual([]string(fromStr), []string{"x", "y"}) {
		t.Errorf("Scan(string): got %v", fromStr)
	}
	// unsupported type
	var bad StringSlice
	if err := bad.Scan(42); err == nil {
		t.Error("Scan(int): expected error")
	}
}
