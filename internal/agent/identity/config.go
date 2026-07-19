package identity

import (
	"fmt"
	"os"
	"strings"
)

// RunnerConfigFromEnv reads the agent-runtime env into a RunnerConfig. Callers
// supply their binary's agent-type default and metrics interval, then set the
// Collect / ExecuteScaling callbacks. The hub agent-CA bundle is mandatory
// (inline HUB_CA_PEM or a HUB_CA_FILE path) — without it there is no trust
// anchor to validate the hub's server cert, so this fails loudly rather than
// dialing insecurely.
//
// Env:
//   HUB_ADDR            hub address (default localhost:50059)
//   AGENT_ENROLL_TOKEN  single-use enrollment token (required only on first boot)
//   HUB_CA_PEM          inline agent-CA bundle PEM, OR
//   HUB_CA_FILE         path to the agent-CA bundle PEM
//   AGENT_STATE_DIR     key/cert store dir (default /var/lib/augur-agent)
//   AGENT_TYPE          overrides the per-binary default
//   WORKLOAD_IDS        comma-separated workload ids
func RunnerConfigFromEnv(defaultAgentType string, defaultIntervalSec int) (RunnerConfig, error) {
	hubCAPEM, err := loadHubCA()
	if err != nil {
		return RunnerConfig{}, err
	}
	hostname, _ := os.Hostname()

	return RunnerConfig{
		HubAddr:            getEnv("HUB_ADDR", "localhost:50059"),
		HubCAPEM:           hubCAPEM,
		EnrollToken:        os.Getenv("AGENT_ENROLL_TOKEN"),
		AgentType:          getEnv("AGENT_TYPE", defaultAgentType),
		Hostname:           hostname,
		WorkloadIDs:        splitWorkloadIDs(os.Getenv("WORKLOAD_IDS")),
		StateDir:           getEnv("AGENT_STATE_DIR", "/var/lib/augur-agent"),
		DefaultIntervalSec: defaultIntervalSec,
		RenewAt:            defaultRenewFraction,
	}, nil
}

// loadHubCA resolves the agent-CA bundle from HUB_CA_PEM (inline) or HUB_CA_FILE.
func loadHubCA() (string, error) {
	if pem := os.Getenv("HUB_CA_PEM"); pem != "" {
		return pem, nil
	}
	if path := os.Getenv("HUB_CA_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("identity: read HUB_CA_FILE: %w", err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("identity: HUB_CA_PEM or HUB_CA_FILE must be set (agent-CA trust anchor)")
}

func splitWorkloadIDs(csv string) []string {
	out := make([]string, 0)
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
