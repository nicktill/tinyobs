package tracing

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

// Handler handles tracing API endpoints
type Handler struct {
	storage *Storage
}

// NewHandler creates a new tracing handler
func NewHandler(storage *Storage) *Handler {
	return &Handler{
		storage: storage,
	}
}

// HandleGetTrace retrieves a specific trace by ID
// GET /v1/traces/{trace_id}
func (h *Handler) HandleGetTrace(w http.ResponseWriter, r *http.Request) {
	// Extract trace ID from URL path using gorilla/mux
	vars := mux.Vars(r)
	traceIDStr := vars["trace_id"]
	if traceIDStr == "" {
		http.Error(w, "trace_id is required", http.StatusBadRequest)
		return
	}

	trace, err := h.storage.GetTrace(r.Context(), TraceID(traceIDStr))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(trace); err != nil {
		log.Printf("❌ Failed to encode trace response: %v", err)
	}
}

// HandleQueryTraces queries traces by time range
// GET /v1/traces?start=<time>&end=<time>&limit=<n>
func (h *Handler) HandleQueryTraces(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// Parse time range
	start := parseTimeParam(query.Get("start"), time.Now().Add(-1*time.Hour))
	end := parseTimeParam(query.Get("end"), time.Now())

	// Parse limit
	limit := 100 // Default limit
	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
			if limit > 1000 {
				limit = 1000 // Max limit
			}
		}
	}

	traces, err := h.storage.QueryTraces(r.Context(), start, end, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"traces": traces,
		"count":  len(traces),
	}); err != nil {
		log.Printf("❌ Failed to encode traces response: %v", err)
	}
}

// HandleRecentTraces returns the most recent traces
// GET /v1/traces/recent?limit=<n>
func (h *Handler) HandleRecentTraces(w http.ResponseWriter, r *http.Request) {
	limit := 50 // Default
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	traces, err := h.storage.GetRecentTraces(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"traces": traces,
		"count":  len(traces),
	}); err != nil {
		log.Printf("❌ Failed to encode recent traces response: %v", err)
	}
}

// HandleTracingStats returns tracing storage statistics
// GET /v1/traces/stats
func (h *Handler) HandleTracingStats(w http.ResponseWriter, r *http.Request) {
	stats := h.storage.Stats()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("❌ Failed to encode tracing stats response: %v", err)
	}
}

// HandleIngestSpan handles manual span ingestion (for testing or external tracers)
// POST /v1/traces/ingest
func (h *Handler) HandleIngestSpan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var span Span
	if err := json.NewDecoder(r.Body).Decode(&span); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.storage.StoreSpan(r.Context(), &span); err != nil {
		http.Error(w, "Failed to store span: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":   "success",
		"trace_id": string(span.TraceID),
		"span_id":  string(span.SpanID),
	}); err != nil {
		log.Printf("❌ Failed to encode ingest span response: %v", err)
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

	// Try Unix timestamp
	if ts, err := strconv.ParseInt(param, 10, 64); err == nil {
		return time.Unix(ts, 0)
	}

	return defaultTime
}
