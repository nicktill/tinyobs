package query

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/storage"
)

// Handler handles query execution requests
type Handler struct {
	executor *Executor
}

// NewHandler creates a new query handler
func NewHandler(store storage.Storage) *Handler {
	return &Handler{
		executor: NewExecutor(store),
	}
}

// QueryRequest represents the request payload for /v1/query/execute
type QueryRequest struct {
	Query string    `json:"query"`           // PromQL-like query string
	Start time.Time `json:"start,omitempty"` // Start time (optional, defaults to now - 1h)
	End   time.Time `json:"end,omitempty"`   // End time (optional, defaults to now)
	Step  string    `json:"step,omitempty"`  // Step duration (optional, defaults to 15s)
}

// QueryResponse represents the response payload
type QueryResponse struct {
	Status string         `json:"status"`
	Data   *ResultData    `json:"data,omitempty"`
	Error  string         `json:"error,omitempty"`
	Query  string         `json:"query"` // Echo back the query
}

// ResultData contains the query result in Prometheus-compatible format
type ResultData struct {
	ResultType string         `json:"resultType"` // "matrix" or "vector"
	Result     []SeriesResult `json:"result"`
}

// SeriesResult represents a single time series result
type SeriesResult struct {
	Metric map[string]string `json:"metric"` // Label set
	Values [][]interface{}   `json:"values"` // [[timestamp, value], ...]
}

const (
	defaultStep         = 15 * time.Second
	defaultQueryWindow  = 1 * time.Hour
	queryTimeout        = 30 * time.Second
)

// HandleQueryExecute handles the /v1/query/execute endpoint
func (h *Handler) HandleQueryExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Validate query
	if req.Query == "" {
		respondError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Set defaults
	now := time.Now()
	if req.Start.IsZero() {
		req.Start = now.Add(-defaultQueryWindow)
	}
	if req.End.IsZero() {
		req.End = now
	}

	// Parse step duration
	step := defaultStep
	if req.Step != "" {
		parsedStep, err := time.ParseDuration(req.Step)
		if err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid step duration: %v", err))
			return
		}
		step = parsedStep
	}

	// Validate time range
	if !req.Start.Before(req.End) {
		respondError(w, http.StatusBadRequest, "start must be before end")
		return
	}

	// Parse query
	parser := NewParser(req.Query)
	expr, err := parser.Parse()
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Query parse error: %v", err))
		return
	}

	// Build query object
	query := &Query{
		Expr:  expr,
		Start: req.Start,
		End:   req.End,
		Step:  step,
	}

	// Execute query with timeout
	ctx, cancel := r.Context(), func() {}
	if r.Context().Err() == nil {
		ctx, cancel = r.Context(), cancel
	}
	defer cancel()

	result, err := h.executor.Execute(ctx, query)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Query execution error: %v", err))
		return
	}

	// Convert result to response format
	response := QueryResponse{
		Status: "success",
		Query:  req.Query,
		Data: &ResultData{
			ResultType: "matrix",
			Result:     convertToSeriesResults(result),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Failed to encode query response: %v", err)
	}
}

// HandleQueryInstant handles the /v1/query endpoint (instant queries)
// This is for compatibility with Prometheus query API
func (h *Handler) HandleQueryInstant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	query := r.URL.Query().Get("query")
	if query == "" {
		respondError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Parse time parameter (defaults to now)
	timeParam := r.URL.Query().Get("time")
	queryTime := time.Now()
	if timeParam != "" {
		if t, err := time.Parse(time.RFC3339, timeParam); err == nil {
			queryTime = t
		}
	}

	// For instant queries, start and end are the same
	req := QueryRequest{
		Query: query,
		Start: queryTime,
		End:   queryTime,
		Step:  "1s", // Minimal step for instant query
	}

	// Parse query
	parser := NewParser(req.Query)
	expr, err := parser.Parse()
	if err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Query parse error: %v", err))
		return
	}

	// Build query object
	queryObj := &Query{
		Expr:  expr,
		Start: req.Start,
		End:   req.End,
		Step:  time.Second,
	}

	// Execute query
	ctx := r.Context()
	result, err := h.executor.Execute(ctx, queryObj)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Query execution error: %v", err))
		return
	}

	// Convert to instant query format (single values, not ranges)
	response := QueryResponse{
		Status: "success",
		Query:  req.Query,
		Data: &ResultData{
			ResultType: "vector",
			Result:     convertToInstantResults(result),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Failed to encode query response: %v", err)
	}
}

// convertToSeriesResults converts executor result to API response format
func convertToSeriesResults(result *Result) []SeriesResult {
	results := make([]SeriesResult, len(result.Series))
	for i, ts := range result.Series {
		values := make([][]interface{}, len(ts.Points))
		for j, p := range ts.Points {
			// Format: [timestamp_unix, value_string]
			values[j] = []interface{}{
				float64(p.Time.Unix()),
				fmt.Sprintf("%.6f", p.Value),
			}
		}
		results[i] = SeriesResult{
			Metric: ts.Labels,
			Values: values,
		}
	}
	return results
}

// convertToInstantResults converts executor result to instant query format
func convertToInstantResults(result *Result) []SeriesResult {
	results := make([]SeriesResult, len(result.Series))
	for i, ts := range result.Series {
		// For instant queries, return only the last value
		var values [][]interface{}
		if len(ts.Points) > 0 {
			lastPoint := ts.Points[len(ts.Points)-1]
			values = [][]interface{}{
				{
					float64(lastPoint.Time.Unix()),
					fmt.Sprintf("%.6f", lastPoint.Value),
				},
			}
		}
		results[i] = SeriesResult{
			Metric: ts.Labels,
			Values: values,
		}
	}
	return results
}

// respondError sends an error response
func respondError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	response := QueryResponse{
		Status: "error",
		Error:  message,
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Failed to encode error response: %v", err)
	}
}
