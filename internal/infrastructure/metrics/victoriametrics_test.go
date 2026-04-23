package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFormatLine(t *testing.T) {
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	expectedTS := fmt.Sprintf("%d", ts.UnixMilli())

	// No labels
	line := formatLine("test_metric", nil, 42.5, ts)
	if !strings.HasPrefix(line, "test_metric 42.5 ") {
		t.Errorf("no-labels: got %q", line)
	}
	if !strings.HasSuffix(line, expectedTS) {
		t.Errorf("no-labels: expected timestamp suffix %s, got %q", expectedTS, line)
	}

	// With labels
	line = formatLine("cpu_pct", map[string]string{"host": "web-1"}, 85.3, ts)
	if !strings.Contains(line, `host="web-1"`) {
		t.Errorf("missing label: got %q", line)
	}
	if !strings.Contains(line, "cpu_pct{") {
		t.Errorf("missing metric with braces: got %q", line)
	}
	if !strings.Contains(line, "85.3") {
		t.Errorf("missing value: got %q", line)
	}
	if !strings.HasSuffix(line, expectedTS) {
		t.Errorf("with-labels: expected timestamp suffix %s, got %q", expectedTS, line)
	}
}

func TestVictoriaMetricsClient_FlushSendsData(t *testing.T) {
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewVictoriaMetricsClient(Config{
		URL:       server.URL,
		FlushSize: 1000,
	})

	ts := time.Now()
	client.Push("test_metric", map[string]string{"env": "prod"}, 42.0, ts)
	client.Push("test_metric2", nil, 99.0, ts)

	err := client.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush() error: %v", err)
	}

	if !strings.Contains(receivedBody, "test_metric") {
		t.Errorf("body missing test_metric: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, "test_metric2") {
		t.Errorf("body missing test_metric2: %s", receivedBody)
	}
}

func TestVictoriaMetricsClient_EmptyFlush(t *testing.T) {
	client := NewVictoriaMetricsClient(Config{URL: "http://unused"})
	err := client.Flush(context.Background())
	if err != nil {
		t.Errorf("empty flush should not error: %v", err)
	}
}

func TestVictoriaMetricsClient_PushWorkloadMetrics(t *testing.T) {
	var lineCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lineCount = len(strings.Split(strings.TrimSpace(string(body)), "\n"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewVictoriaMetricsClient(Config{
		URL:       server.URL,
		FlushSize: 1000,
	})

	client.PushWorkloadMetrics(
		"wl-1", "api-gateway", "kubernetes", "prod", "org-1",
		75.5, 60.2, 1500, 250, 0.5, 3,
		time.Now(),
	)

	client.Flush(context.Background())

	// PushWorkloadMetrics pushes 6 metrics
	if lineCount != 6 {
		t.Errorf("expected 6 metric lines, got %d", lineCount)
	}
}

func TestVictoriaMetricsClient_MaxBufferDrop(t *testing.T) {
	client := NewVictoriaMetricsClient(Config{
		URL:        "http://unused",
		FlushSize:  1000,
		MaxBufSize: 5,
	})

	ts := time.Now()
	for i := range 10 {
		client.Push("metric", nil, float64(i), ts)
	}

	client.mu.Lock()
	bufLen := len(client.buffer)
	client.mu.Unlock()

	if bufLen > 5 {
		t.Errorf("buffer should be capped at 5, got %d", bufLen)
	}
}

func TestVictoriaMetricsClient_AuthHeader(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewVictoriaMetricsClient(Config{
		URL:       server.URL,
		AuthToken: "my-secret-token",
		FlushSize: 1000,
	})

	client.Push("metric", nil, 1.0, time.Now())
	client.Flush(context.Background())

	if authHeader != "Bearer my-secret-token" {
		t.Errorf("expected auth header 'Bearer my-secret-token', got %q", authHeader)
	}
}
