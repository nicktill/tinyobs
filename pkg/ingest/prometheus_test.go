package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage/memory"
)

func TestHandlePrometheusMetrics(t *testing.T) {
	// Create a storage with some test metrics
	store := memory.New()
	ctx := context.Background()
	now := time.Now()

	testMetrics := []metrics.Metric{
		{
			Name:      "http_requests_total",
			Type:      metrics.CounterType,
			Value:     42,
			Labels:    map[string]string{"endpoint": "/api", "status": "200"},
			Timestamp: now,
		},
		{
			Name:      "http_request_duration_seconds",
			Type:      metrics.HistogramType,
			Value:     0.123,
			Labels:    map[string]string{"endpoint": "/api"},
			Timestamp: now,
		},
		{
			Name:      "cpu_usage",
			Type:      metrics.GaugeType,
			Value:     75.5,
			Labels:    map[string]string{"host": "server1"},
			Timestamp: now,
		},
	}

	err := store.Write(ctx, testMetrics)
	if err != nil {
		t.Fatalf("Failed to write test metrics: %v", err)
	}

	// Create handler
	handler := NewHandler(store)

	// Make request
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.HandlePrometheusMetrics(rec, req)

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Expected Content-Type to contain 'text/plain', got %s", contentType)
	}

	body := rec.Body.String()

	// Verify HELP and TYPE comments are present
	if !strings.Contains(body, "# HELP http_requests_total") {
		t.Errorf("Expected HELP comment for http_requests_total")
	}
	if !strings.Contains(body, "# TYPE http_requests_total") {
		t.Errorf("Expected TYPE comment for http_requests_total")
	}

	// Verify metric data is present
	if !strings.Contains(body, "http_requests_total") {
		t.Errorf("Expected http_requests_total metric in output")
	}
	if !strings.Contains(body, "endpoint=\"/api\"") {
		t.Errorf("Expected endpoint label in output")
	}
	if !strings.Contains(body, "status=\"200\"") {
		t.Errorf("Expected status label in output")
	}

	// Verify all metrics are present
	expectedMetrics := []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"cpu_usage",
	}
	for _, metricName := range expectedMetrics {
		if !strings.Contains(body, metricName) {
			t.Errorf("Expected metric %s in output, got: %s", metricName, body)
		}
	}
}

func TestHandlePrometheusMetrics_EmptyStorage(t *testing.T) {
	// Create empty storage
	store := memory.New()
	handler := NewHandler(store)

	// Make request
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.HandlePrometheusMetrics(rec, req)

	// Should still return 200 OK with empty metrics
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for empty storage, got %d", rec.Code)
	}

	body := rec.Body.String()
	// Body should be empty or only contain comments
	if strings.Contains(body, "http_requests_total") {
		t.Errorf("Expected no metrics in empty storage, got: %s", body)
	}
}

func TestGroupMetricsByName(t *testing.T) {
	testMetrics := []metrics.Metric{
		{Name: "metric_a", Value: 1.0},
		{Name: "metric_b", Value: 2.0},
		{Name: "metric_a", Value: 3.0},
		{Name: "metric_c", Value: 4.0},
		{Name: "metric_b", Value: 5.0},
	}

	grouped := groupMetricsByName(testMetrics)

	// Should have 3 groups
	if len(grouped) != 3 {
		t.Errorf("Expected 3 groups, got %d", len(grouped))
	}

	// metric_a should have 2 entries
	if len(grouped["metric_a"]) != 2 {
		t.Errorf("Expected 2 entries for metric_a, got %d", len(grouped["metric_a"]))
	}

	// metric_b should have 2 entries
	if len(grouped["metric_b"]) != 2 {
		t.Errorf("Expected 2 entries for metric_b, got %d", len(grouped["metric_b"]))
	}

	// metric_c should have 1 entry
	if len(grouped["metric_c"]) != 1 {
		t.Errorf("Expected 1 entry for metric_c, got %d", len(grouped["metric_c"]))
	}
}

func TestInferPrometheusType(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []metrics.Metric
		expected string
	}{
		{
			name: "counter type in labels",
			metrics: []metrics.Metric{
				{Labels: map[string]string{"type": "counter"}},
			},
			expected: "counter",
		},
		{
			name: "gauge type in labels",
			metrics: []metrics.Metric{
				{Labels: map[string]string{"type": "gauge"}},
			},
			expected: "gauge",
		},
		{
			name:     "no type label - default to gauge",
			metrics:  []metrics.Metric{{Labels: map[string]string{}}},
			expected: "gauge",
		},
		{
			name:     "empty metrics - untyped",
			metrics:  []metrics.Metric{},
			expected: "untyped",
		},
		{
			name:     "nil labels - default to gauge",
			metrics:  []metrics.Metric{{Labels: nil}},
			expected: "gauge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferPrometheusType(tt.metrics)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestFormatPrometheusLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name:     "empty labels",
			labels:   map[string]string{},
			expected: "",
		},
		{
			name:     "nil labels",
			labels:   nil,
			expected: "",
		},
		{
			name:     "single label",
			labels:   map[string]string{"host": "server1"},
			expected: `{host="server1"}`,
		},
		{
			name:     "multiple labels - sorted",
			labels:   map[string]string{"host": "server1", "app": "api"},
			expected: `{app="api",host="server1"}`, // Sorted alphabetically
		},
		{
			name:     "label with special characters",
			labels:   map[string]string{"path": "/api/users"},
			expected: `{path="/api/users"}`,
		},
		{
			name:     "internal labels filtered out",
			labels:   map[string]string{"_internal": "hidden", "visible": "shown"},
			expected: `{visible="shown"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPrometheusLabels(tt.labels)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestEscapePrometheusValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no escaping needed",
			input:    "simple_value",
			expected: "simple_value",
		},
		{
			name:     "backslash",
			input:    `path\with\backslash`,
			expected: `path\\with\\backslash`,
		},
		{
			name:     "double quote",
			input:    `value"with"quotes`,
			expected: `value\"with\"quotes`,
		},
		{
			name:     "line feed",
			input:    "value\nwith\nnewline",
			expected: `value\nwith\nnewline`,
		},
		{
			name:     "all special characters",
			input:    "test\\\n\"",
			expected: `test\\\n\"`, // backslash-backslash-newline-quote
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapePrometheusValue(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}
