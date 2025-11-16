package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// Handler handles metric ingestion
type Handler struct {
	storage     storage.Storage
	cardinality *CardinalityTracker
}

// NewHandler creates a new ingest handler
func NewHandler(store storage.Storage) *Handler {
	return &Handler{
		storage:     store,
		cardinality: NewCardinalityTracker(),
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

	// Check request size limit
	if len(req.Metrics) > MaxMetricsPerRequest {
		http.Error(w, ErrTooManyMetrics.Error(), http.StatusBadRequest)
		return
	}

	// Validate metrics and check cardinality
	now := time.Now()
	for i := range req.Metrics {
		// Set timestamp if not provided
		if req.Metrics[i].Timestamp.IsZero() {
			req.Metrics[i].Timestamp = now
		}

		// Validate metric format
		if err := ValidateMetric(req.Metrics[i]); err != nil {
			http.Error(w, fmt.Sprintf("Invalid metric at index %d: %v", i, err), http.StatusBadRequest)
			return
		}

		// Check cardinality limits
		if err := h.cardinality.Check(req.Metrics[i]); err != nil {
			http.Error(w, fmt.Sprintf("Cardinality limit exceeded for metric %q: %v", req.Metrics[i].Name, err), http.StatusTooManyRequests)
			return
		}
	}

	// Store metrics
	ctx, cancel := context.WithTimeout(r.Context(), ingestTimeout)
	defer cancel()

	if err := h.storage.Write(ctx, req.Metrics); err != nil {
		http.Error(w, fmt.Sprintf("Failed to store metrics: %v", err), http.StatusInternalServerError)
		return
	}

	// Record successfully written metrics for cardinality tracking
	for _, m := range req.Metrics {
		h.cardinality.Record(m)
	}

	// Respond
	response := IngestResponse{
		Status: "success",
		Count:  len(req.Metrics),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Failed to encode ingest response: %v", err)
	}
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
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"metrics": results,
		"count":   len(results),
	}); err != nil {
		log.Printf("❌ Failed to encode query response: %v", err)
	}
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
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("❌ Failed to encode stats response: %v", err)
	}
}

// HandleCardinalityStats handles the /v1/cardinality endpoint
// Returns cardinality usage statistics for monitoring
func (h *Handler) HandleCardinalityStats(w http.ResponseWriter, r *http.Request) {
	stats := h.cardinality.Stats()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("❌ Failed to encode cardinality stats response: %v", err)
	}
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
	log.Printf("⚠️  Invalid time format %q, using default. Expected RFC3339 or 2006-01-02T15:04:05", param)
	return defaultTime
}
