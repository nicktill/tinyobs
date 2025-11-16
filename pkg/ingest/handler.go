package ingest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"tinyobs/pkg/sdk/metrics"
)

// Handler handles metric ingestion
type Handler struct {
	metrics []metrics.Metric
	mu      sync.RWMutex
}

// NewHandler creates a new ingest handler
func NewHandler() *Handler {
	return &Handler{
		metrics: make([]metrics.Metric, 0),
	}
}

// IngestRequest represents the request payload
type IngestRequest struct {
	Metrics []metrics.Metric `json:"metrics"`
}

// IngestResponse represents the response payload
type IngestResponse struct {
	Status  string `json:"status"`
	Count   int    `json:"count"`
	Message string `json:"message,omitempty"`
}

// HandleIngest handles the /v1/ingest endpoint
func (h *Handler) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Store metrics
	h.mu.Lock()
	h.metrics = append(h.metrics, req.Metrics...)
	h.mu.Unlock()

	// Respond
	response := IngestResponse{
		Status: "success",
		Count:  len(req.Metrics),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetMetrics returns all stored metrics
func (h *Handler) GetMetrics() []metrics.Metric {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	// Return a copy to avoid race conditions
	metrics := make([]metrics.Metric, len(h.metrics))
	copy(metrics, h.metrics)
	return metrics
}

// GetMetricsByService returns metrics for a specific service
func (h *Handler) GetMetricsByService(service string) []metrics.Metric {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	var filtered []metrics.Metric
	for _, metric := range h.metrics {
		if metric.Labels != nil && metric.Labels["service"] == service {
			filtered = append(filtered, metric)
		}
	}
	return filtered
}

// GetRecentMetrics returns metrics from the last N minutes
func (h *Handler) GetRecentMetrics(minutes int) []metrics.Metric {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	cutoff := time.Now().Add(-time.Duration(minutes) * time.Minute)
	var recent []metrics.Metric
	
	for _, metric := range h.metrics {
		if metric.Timestamp.After(cutoff) {
			recent = append(recent, metric)
		}
	}
	return recent
}

// ClearMetrics clears all stored metrics
func (h *Handler) ClearMetrics() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.metrics = h.metrics[:0]
}


