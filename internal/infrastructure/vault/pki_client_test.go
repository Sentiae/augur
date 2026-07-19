package vault

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAgentSPIFFEURI(t *testing.T) {
	org := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agent := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	got := AgentSPIFFEURI(org, agent)
	want := "spiffe://sentiae/11111111-1111-1111-1111-111111111111/agent/22222222-2222-2222-2222-222222222222"
	if got != want {
		t.Fatalf("AgentSPIFFEURI = %q, want %q", got, want)
	}
}

func TestBuildSignRequest(t *testing.T) {
	org := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agent := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	csr := "-----BEGIN CERTIFICATE REQUEST-----\nMII...\n-----END CERTIFICATE REQUEST-----"

	req := buildSignRequest(csr, org, agent, time.Hour)

	if req["csr"] != csr {
		t.Errorf("csr = %v, want %v", req["csr"], csr)
	}
	if req["common_name"] != agent.String() {
		t.Errorf("common_name = %v, want %v", req["common_name"], agent.String())
	}
	if req["uri_sans"] != AgentSPIFFEURI(org, agent) {
		t.Errorf("uri_sans = %v, want %v", req["uri_sans"], AgentSPIFFEURI(org, agent))
	}
	if req["ttl"] != "1h0m0s" {
		t.Errorf("ttl = %v, want 1h0m0s", req["ttl"])
	}
	if req["format"] != "pem" {
		t.Errorf("format = %v, want pem", req["format"])
	}
}

func TestBuildHubIssueRequest(t *testing.T) {
	req := buildHubIssueRequest("augur-hub", []string{"10.0.10.20", "127.0.0.1"}, []string{"augur", "localhost"}, time.Hour)

	if req["common_name"] != "augur-hub" {
		t.Errorf("common_name = %v, want augur-hub", req["common_name"])
	}
	if req["ip_sans"] != "10.0.10.20,127.0.0.1" {
		t.Errorf("ip_sans = %v, want 10.0.10.20,127.0.0.1", req["ip_sans"])
	}
	if req["alt_names"] != "augur,localhost" {
		t.Errorf("alt_names = %v, want augur,localhost", req["alt_names"])
	}
	if req["ttl"] != "1h0m0s" {
		t.Errorf("ttl = %v, want 1h0m0s", req["ttl"])
	}
	if req["format"] != "pem" {
		t.Errorf("format = %v, want pem", req["format"])
	}
}

func TestParseIssueResponse(t *testing.T) {
	tests := []struct {
		name      string
		data      map[string]any
		wantCert  string
		wantKey   string
		wantChain string
		wantErr   bool
	}{
		{
			name: "cert + key + ca_chain",
			data: map[string]any{
				"certificate": "LEAF",
				"private_key": "KEY",
				"ca_chain":    []any{"INT", "ROOT"},
			},
			wantCert:  "LEAF",
			wantKey:   "KEY",
			wantChain: "INT\nROOT",
		},
		{
			name: "falls back to issuing_ca",
			data: map[string]any{
				"certificate": "LEAF",
				"private_key": "KEY",
				"issuing_ca":  "ROOT",
			},
			wantCert:  "LEAF",
			wantKey:   "KEY",
			wantChain: "ROOT",
		},
		{
			name:    "missing certificate",
			data:    map[string]any{"private_key": "KEY", "issuing_ca": "ROOT"},
			wantErr: true,
		},
		{
			name:    "missing private_key",
			data:    map[string]any{"certificate": "LEAF", "issuing_ca": "ROOT"},
			wantErr: true,
		},
		{
			name:    "missing chain",
			data:    map[string]any{"certificate": "LEAF", "private_key": "KEY"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert, key, chain, err := parseIssueResponse(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got cert=%q key=%q chain=%q", cert, key, chain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cert != tt.wantCert || key != tt.wantKey || chain != tt.wantChain {
				t.Errorf("got (%q,%q,%q), want (%q,%q,%q)", cert, key, chain, tt.wantCert, tt.wantKey, tt.wantChain)
			}
		})
	}
}

func TestParseSignResponse(t *testing.T) {
	tests := []struct {
		name      string
		data      map[string]any
		wantCert  string
		wantChain string
		wantErr   bool
	}{
		{
			name: "ca_chain as []any (Vault JSON decode)",
			data: map[string]any{
				"certificate": "LEAF",
				"ca_chain":    []any{"INT", "ROOT"},
			},
			wantCert:  "LEAF",
			wantChain: "INT\nROOT",
		},
		{
			name: "ca_chain as []string",
			data: map[string]any{
				"certificate": "LEAF",
				"ca_chain":    []string{"INT", "ROOT"},
			},
			wantCert:  "LEAF",
			wantChain: "INT\nROOT",
		},
		{
			name: "falls back to issuing_ca when no ca_chain",
			data: map[string]any{
				"certificate": "LEAF",
				"issuing_ca":  "ROOT",
			},
			wantCert:  "LEAF",
			wantChain: "ROOT",
		},
		{
			name:    "missing certificate",
			data:    map[string]any{"ca_chain": []any{"ROOT"}},
			wantErr: true,
		},
		{
			name:    "empty certificate",
			data:    map[string]any{"certificate": "", "ca_chain": []any{"ROOT"}},
			wantErr: true,
		},
		{
			name:    "missing chain and issuing_ca",
			data:    map[string]any{"certificate": "LEAF"},
			wantErr: true,
		},
		{
			name: "empty ca_chain entries and no issuing_ca",
			data: map[string]any{
				"certificate": "LEAF",
				"ca_chain":    []any{"", ""},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert, chain, err := parseSignResponse(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got cert=%q chain=%q", cert, chain)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cert != tt.wantCert {
				t.Errorf("cert = %q, want %q", cert, tt.wantCert)
			}
			if chain != tt.wantChain {
				t.Errorf("chain = %q, want %q", chain, tt.wantChain)
			}
		})
	}
}
