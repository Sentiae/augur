package metrics

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sentiae/infrastructure-intelligence-service/pkg/logger"
)

// VictoriaMetricsClient pushes metrics to VictoriaMetrics using the Prometheus
// import API (line protocol). This is simpler than remote-write protobuf and
// supported by both VictoriaMetrics and Prometheus.
type VictoriaMetricsClient struct {
	importURL  string
	httpClient *http.Client
	authToken  string

	mu      sync.Mutex
	buffer  []string
	maxBuf  int
	flushAt int
}

// Config holds VictoriaMetrics connection settings.
type Config struct {
	URL          string        // e.g., "http://victoriametrics:8428"
	AuthToken    string        // optional bearer token
	Timeout      time.Duration // HTTP timeout
	FlushSize    int           // flush after this many lines (default 100)
	MaxBufSize   int           // max buffer before dropping (default 10000)
}

// NewVictoriaMetricsClient creates a new metrics push client.
func NewVictoriaMetricsClient(cfg Config) *VictoriaMetricsClient {
	if cfg.FlushSize <= 0 {
		cfg.FlushSize = 100
	}
	if cfg.MaxBufSize <= 0 {
		cfg.MaxBufSize = 10000
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	return &VictoriaMetricsClient{
		importURL: cfg.URL + "/api/v1/import/prometheus",
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		authToken: cfg.AuthToken,
		buffer:    make([]string, 0, cfg.FlushSize),
		maxBuf:    cfg.MaxBufSize,
		flushAt:   cfg.FlushSize,
	}
}

// Push adds a metric in Prometheus line format and flushes if buffer is full.
// Format: metric_name{label="value"} float_value timestamp_ms
func (c *VictoriaMetricsClient) Push(name string, labels map[string]string, value float64, ts time.Time) {
	line := formatLine(name, labels, value, ts)

	c.mu.Lock()
	if len(c.buffer) >= c.maxBuf {
		c.mu.Unlock()
		return // drop to prevent OOM
	}
	c.buffer = append(c.buffer, line)
	shouldFlush := len(c.buffer) >= c.flushAt
	c.mu.Unlock()

	if shouldFlush {
		go c.Flush(context.Background())
	}
}

// PushWorkloadMetrics pushes a standard set of workload metrics.
func (c *VictoriaMetricsClient) PushWorkloadMetrics(
	workloadID, workloadName, workloadType, environment, orgID string,
	cpu, memory, rps, latencyP99, errorRate float64,
	replicas int,
	ts time.Time,
) {
	labels := map[string]string{
		"workload_id":   workloadID,
		"workload_name": workloadName,
		"workload_type": workloadType,
		"environment":   environment,
		"org_id":        orgID,
	}

	c.Push("augur_workload_cpu_pct", labels, cpu, ts)
	c.Push("augur_workload_memory_pct", labels, memory, ts)
	c.Push("augur_workload_rps", labels, rps, ts)
	c.Push("augur_workload_latency_p99_ms", labels, latencyP99, ts)
	c.Push("augur_workload_error_rate_pct", labels, errorRate, ts)
	c.Push("augur_workload_replicas", labels, float64(replicas), ts)
}

// PushDecisionMetric records a scaling decision metric.
func (c *VictoriaMetricsClient) PushDecisionMetric(
	workloadID, direction, trigger string,
	fromReplicas, toReplicas int,
	confidence float64,
	ts time.Time,
) {
	labels := map[string]string{
		"workload_id": workloadID,
		"direction":   direction,
		"trigger":     trigger,
	}
	c.Push("augur_scaling_decision", labels, float64(toReplicas-fromReplicas), ts)
	c.Push("augur_scaling_confidence", labels, confidence, ts)
}

// PushCostMetric records cost tracking metrics.
func (c *VictoriaMetricsClient) PushCostMetric(
	scope, scopeID string,
	hourlyUSD, monthlyUSD, budgetUSD float64,
	ts time.Time,
) {
	labels := map[string]string{
		"scope":    scope,
		"scope_id": scopeID,
	}
	c.Push("augur_cost_hourly_usd", labels, hourlyUSD, ts)
	c.Push("augur_cost_monthly_usd", labels, monthlyUSD, ts)
	if budgetUSD > 0 {
		c.Push("augur_cost_budget_usd", labels, budgetUSD, ts)
		c.Push("augur_cost_budget_utilization_pct", labels, (monthlyUSD/budgetUSD)*100, ts)
	}
}

// Flush sends all buffered metrics to VictoriaMetrics.
func (c *VictoriaMetricsClient) Flush(ctx context.Context) error {
	c.mu.Lock()
	if len(c.buffer) == 0 {
		c.mu.Unlock()
		return nil
	}
	lines := c.buffer
	c.buffer = make([]string, 0, c.flushAt)
	c.mu.Unlock()

	body := strings.Join(lines, "\n") + "\n"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.importURL, bytes.NewBufferString(body))
	if err != nil {
		return fmt.Errorf("create flush request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logger.Error("Failed to flush metrics to VictoriaMetrics: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		logger.Error("VictoriaMetrics returned status %d for %d metrics", resp.StatusCode, len(lines))
		return fmt.Errorf("victoriametrics status %d", resp.StatusCode)
	}

	logger.Debug("Flushed %d metrics to VictoriaMetrics", len(lines))
	return nil
}

// Close flushes remaining metrics.
func (c *VictoriaMetricsClient) Close() {
	_ = c.Flush(context.Background())
}

func formatLine(name string, labels map[string]string, value float64, ts time.Time) string {
	if len(labels) == 0 {
		return fmt.Sprintf("%s %g %d", name, value, ts.UnixMilli())
	}

	var lb strings.Builder
	lb.WriteString(name)
	lb.WriteByte('{')
	first := true
	for k, v := range labels {
		if !first {
			lb.WriteByte(',')
		}
		lb.WriteString(k)
		lb.WriteString(`="`)
		lb.WriteString(strings.ReplaceAll(v, `"`, `\"`))
		lb.WriteByte('"')
		first = false
	}
	lb.WriteByte('}')
	return fmt.Sprintf("%s %g %d", lb.String(), value, ts.UnixMilli())
}
