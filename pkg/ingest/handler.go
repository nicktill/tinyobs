package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tinyobs/pkg/sdk/metrics"
	"tinyobs/pkg/storage"
)

// Handler handles metric ingestion
type Handler struct {
	storage storage.Storage
}

// NewHandler creates a new ingest handler
func NewHandler(store storage.Storage) *Handler {
	return &Handler{
		storage: store,
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

	// Validate and set timestamps
	now := time.Now()
	for i := range req.Metrics {
		if req.Metrics[i].Timestamp.IsZero() {
			req.Metrics[i].Timestamp = now
		}
	}

	// Store metrics
	ctx, cancel := context.WithTimeout(r.Context(), ingestTimeout)
	defer cancel()

	if err := h.storage.Write(ctx, req.Metrics); err != nil {
		http.Error(w, fmt.Sprintf("Failed to store metrics: %v", err), http.StatusInternalServerError)
		return
	}

	// Respond
	response := IngestResponse{
		Status: "success",
		Count:  len(req.Metrics),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleQuery handles the /v1/query endpoint
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Build storage query
	req := storage.QueryRequest{
		Start: parseTimeParam(query.Get("start"), time.Now().Add(-defaultQueryWindow)),
		End:   parseTimeParam(query.Get("end"), time.Now()),
	}

	// Validate time range
	if !req.Start.Before(req.End) {
		http.Error(w, "start must be before end", http.StatusBadRequest)
		return
	}

	// Optional filters
	if metricName := query.Get("metric"); metricName != "" {
		req.MetricNames = []string{metricName}
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	results, err := h.storage.Query(ctx, req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Query failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"metrics": results,
		"count":   len(results),
	})
}

// HandleStats handles the /v1/stats endpoint
func (h *Handler) HandleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), statsTimeout)
	defer cancel()

	stats, err := h.storage.Stats(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get stats: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// parseTimeParam parses a time parameter or returns default
func parseTimeParam(param string, defaultTime time.Time) time.Time {
	if param == "" {
		return defaultTime
	}

	// Try RFC3339 format
	if t, err := time.Parse(time.RFC3339, param); err == nil {
		return t
	}

	// Try simple datetime format
	if t, err := time.Parse("2006-01-02T15:04:05", param); err == nil {
		return t
	}

	// Log warning about invalid format
	fmt.Printf("Warning: invalid time format '%s', using default. Expected RFC3339 or 2006-01-02T15:04:05\n", param)
	return defaultTime
}
