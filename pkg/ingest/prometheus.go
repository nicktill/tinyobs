package ingest

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// HandlePrometheusMetrics exports metrics in Prometheus text format
// This allows external tools (Grafana, Prometheus, etc.) to scrape TinyObs
//
// Format: https://prometheus.io/docs/instrumenting/exposition_formats/
func (h *Handler) HandlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), statsTimeout)
	defer cancel()

	// Query recent metrics (last 5 minutes)
	// This gives Grafana fresh data on each scrape
	results, err := h.storage.Query(ctx, storage.QueryRequest{
		Start: time.Now().Add(-5 * time.Minute),
		End:   time.Now(),
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Set Prometheus text format content type
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	// Group metrics by name for better organization
	grouped := groupMetricsByName(results)

	// Export each metric group
	for name, metricGroup := range grouped {
		// Write HELP comment (describes what the metric measures)
		fmt.Fprintf(w, "# HELP %s TinyObs metric\n", name)

		// Write TYPE comment (counter, gauge, histogram, etc.)
		// TinyObs tracks type in labels, default to untyped
		metricType := inferPrometheusType(metricGroup)
		fmt.Fprintf(w, "# TYPE %s %s\n", name, metricType)

		// Write metric samples
		for _, m := range metricGroup {
			// Format: metric_name{label="value"} value timestamp
			fmt.Fprintf(w, "%s%s %v %d\n",
				name,
				formatPrometheusLabels(m.Labels),
				m.Value,
				m.Timestamp.UnixMilli(),
			)
		}

		// Empty line between metric families
		fmt.Fprintf(w, "\n")
	}
}

// groupMetricsByName groups metrics by their name
func groupMetricsByName(ms []metrics.Metric) map[string][]metrics.Metric {
	grouped := make(map[string][]metrics.Metric)
	for _, m := range ms {
		grouped[m.Name] = append(grouped[m.Name], m)
	}
	return grouped
}

// inferPrometheusType infers the Prometheus metric type from metric metadata
func inferPrometheusType(ms []metrics.Metric) string {
	if len(ms) == 0 {
		return "untyped"
	}

	// Check if any metric has a type label
	for _, m := range ms {
		if m.Labels != nil {
			if typ := m.Labels["type"]; typ != "" {
				return typ
			}
		}
	}

	// Default to gauge (most flexible)
	return "gauge"
}

// formatPrometheusLabels formats labels in Prometheus format: {key="value",key2="value2"}
func formatPrometheusLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	// Filter out internal labels (those starting with __)
	userLabels := make(map[string]string)
	for k, v := range labels {
		if len(k) > 0 && k[0] != '_' {
			userLabels[k] = v
		}
	}

	if len(userLabels) == 0 {
		return ""
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(userLabels))
	for k := range userLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build label pairs
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		// Escape special characters in label values
		escapedValue := escapePrometheusValue(userLabels[k])
		pairs = append(pairs, fmt.Sprintf(`%s="%s"`, k, escapedValue))
	}

	return "{" + strings.Join(pairs, ",") + "}"
}

// escapePrometheusValue escapes special characters in Prometheus label values
// Per spec: backslash, double-quote, and line feed must be escaped
func escapePrometheusValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)  // Backslash
	s = strings.ReplaceAll(s, `"`, `\"`)  // Double quote
	s = strings.ReplaceAll(s, "\n", `\n`) // Line feed
	return s
}
