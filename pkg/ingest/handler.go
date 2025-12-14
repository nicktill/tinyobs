package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/config"
	"github.com/nicktill/tinyobs/pkg/httpx"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// Handler handles metric ingestion via HTTP endpoints.
// Validates metrics, enforces cardinality limits, and stores them.
type Handler struct {
	storage        storage.Storage
	cardinality    *CardinalityTracker
	storageChecker StorageLimitChecker
}

// StorageLimitChecker provides storage usage information for limit enforcement.
type StorageLimitChecker interface {
	// GetUsage returns current storage usage in bytes.
	GetUsage() (int64, error)
	// GetLimit returns the configured storage limit in bytes.
	GetLimit() int64
}

// NewHandler creates a new ingest handler with the given storage backend.
func NewHandler(store storage.Storage) *Handler {
	return &Handler{
		storage:        store,
		cardinality:    NewCardinalityTracker(),
		storageChecker: nil, // Optional - can be set via SetStorageChecker
	}
}

// SetStorageChecker configures storage limit checking for the handler.
// If set, HandleIngest will reject metrics when storage limit is exceeded.
func (h *Handler) SetStorageChecker(checker StorageLimitChecker) {
	h.storageChecker = checker
}

// IngestRequest represents the request payload for POST /v1/ingest.
type IngestRequest struct {
	Metrics []metrics.Metric `json:"metrics"` // Array of metrics to ingest
}

// IngestResponse represents the response payload for ingestion endpoints.
type IngestResponse struct {
	Status  string `json:"status"`           // "success" or "error"
	Count   int    `json:"count"`            // Number of metrics ingested
	Message string `json:"message,omitempty"` // Optional error or info message
}

// HandleIngest handles POST /v1/ingest.
// Validates metrics, checks cardinality and storage limits, then stores them.
func (h *Handler) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	// Check request size limit
	if len(req.Metrics) > MaxMetricsPerRequest {
		httpx.RespondError(w, http.StatusBadRequest, ErrTooManyMetrics)
		return
	}

	// Check storage limits BEFORE processing (fail fast to prevent disk overflow)
	if h.storageChecker != nil {
		currentUsage, err := h.storageChecker.GetUsage()
		if err != nil {
			log.Printf("Failed to check storage usage: %v", err)
			// Continue anyway - don't block ingestion on monitoring failure
		} else {
			limit := h.storageChecker.GetLimit()
			if currentUsage >= limit {
				// 507 Insufficient Storage (WebDAV standard, appropriate for storage limits)
				message := fmt.Sprintf("Storage limit exceeded: %d/%d bytes used (%.1f%%). Please free up space or increase limit.",
					currentUsage, limit, float64(currentUsage)/float64(limit)*100)
				log.Printf("STORAGE LIMIT EXCEEDED: %d/%d bytes (%.1f%%) - rejecting ingest",
					currentUsage, limit, float64(currentUsage)/float64(limit)*100)
				httpx.RespondErrorString(w, 507, message)
				return
			}
		}
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
			httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("invalid metric at index %d: %w", i, err))
			return
		}

		// Check cardinality limits
		if err := h.cardinality.Check(req.Metrics[i]); err != nil {
			httpx.RespondError(w, http.StatusTooManyRequests, fmt.Errorf("cardinality limit exceeded for metric %q: %w", req.Metrics[i].Name, err))
			return
		}
	}

	// Store metrics
	ctx, cancel := context.WithTimeout(r.Context(), config.IngestTimeout)
	defer cancel()

	if err := h.storage.Write(ctx, req.Metrics); err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("failed to store metrics: %w", err))
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

	httpx.RespondJSON(w, http.StatusOK, response)
}

// QueryResponse represents the response for a query request.
type QueryResponse struct {
	Metrics []metrics.Metric `json:"metrics"`
	Count   int              `json:"count"`
}

// HandleQuery handles the /v1/query endpoint.
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Build storage query
	req := storage.QueryRequest{
		Start: parseTimeParam(query.Get("start"), time.Now().Add(-config.IngestDefaultQueryWindow)),
		End:   parseTimeParam(query.Get("end"), time.Now()),
	}

	// Validate time range
	if !req.Start.Before(req.End) {
		httpx.RespondErrorString(w, http.StatusBadRequest, "start must be before end")
		return
	}

	// Optional filters
	if metricName := query.Get("metric"); metricName != "" {
		req.MetricNames = []string{metricName}
	}

	ctx, cancel := context.WithTimeout(r.Context(), config.IngestQueryTimeout)
	defer cancel()

	results, err := h.storage.Query(ctx, req)
	if err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("query failed: %w", err))
		return
	}

	response := QueryResponse{
		Metrics: results,
		Count:   len(results),
	}

	httpx.RespondJSON(w, http.StatusOK, response)
}

// HandleStats handles the /v1/stats endpoint.
func (h *Handler) HandleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), config.IngestStatsTimeout)
	defer cancel()

	stats, err := h.storage.Stats(ctx)
	if err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("failed to get stats: %w", err))
		return
	}

	httpx.RespondJSON(w, http.StatusOK, stats)
}

// HandleCardinalityStats handles the /v1/cardinality endpoint.
// Returns cardinality usage statistics for monitoring.
func (h *Handler) HandleCardinalityStats(w http.ResponseWriter, r *http.Request) {
	stats := h.cardinality.Stats()
	httpx.RespondJSON(w, http.StatusOK, stats)
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
	log.Printf("Invalid time format %q, using default. Expected RFC3339 or 2006-01-02T15:04:05", param)
	return defaultTime
}
