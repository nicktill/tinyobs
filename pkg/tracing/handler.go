package tracing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/nicktill/tinyobs/pkg/httpx"
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

// TracesResponse represents a response containing multiple traces.
type TracesResponse struct {
	Traces []*Trace `json:"traces"`
	Count  int      `json:"count"`
}

// IngestSpanResponse represents the response for span ingestion.
type IngestSpanResponse struct {
	Status  string `json:"status"`
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

// HandleGetTrace retrieves a specific trace by ID.
// GET /v1/traces/{trace_id}
func (h *Handler) HandleGetTrace(w http.ResponseWriter, r *http.Request) {
	// Extract trace ID from URL path using gorilla/mux
	vars := mux.Vars(r)
	traceIDStr := vars["trace_id"]
	if traceIDStr == "" {
		httpx.RespondErrorString(w, http.StatusBadRequest, "trace_id is required")
		return
	}

	trace, err := h.storage.GetTrace(r.Context(), TraceID(traceIDStr))
	if err != nil {
		httpx.RespondError(w, http.StatusNotFound, err)
		return
	}

	httpx.RespondJSON(w, http.StatusOK, trace)
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
		httpx.RespondError(w, http.StatusInternalServerError, err)
		return
	}

	response := TracesResponse{
		Traces: traces,
		Count:  len(traces),
	}
	httpx.RespondJSON(w, http.StatusOK, response)
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
		httpx.RespondError(w, http.StatusInternalServerError, err)
		return
	}

	response := TracesResponse{
		Traces: traces,
		Count:  len(traces),
	}
	httpx.RespondJSON(w, http.StatusOK, response)
}

// HandleTracingStats returns tracing storage statistics.
// GET /v1/traces/stats
func (h *Handler) HandleTracingStats(w http.ResponseWriter, r *http.Request) {
	stats := h.storage.Stats()
	httpx.RespondJSON(w, http.StatusOK, stats)
}

// HandleIngestSpan handles manual span ingestion (for testing or external tracers).
// POST /v1/traces/ingest
func (h *Handler) HandleIngestSpan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var span Span
	if err := json.NewDecoder(r.Body).Decode(&span); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	if err := h.storage.StoreSpan(r.Context(), &span); err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("failed to store span: %w", err))
		return
	}

	response := IngestSpanResponse{
		Status:  "success",
		TraceID: string(span.TraceID),
		SpanID:  string(span.SpanID),
	}
	httpx.RespondJSON(w, http.StatusCreated, response)
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
