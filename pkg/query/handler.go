package query

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/nicktill/tinyobs/pkg/config"
	"github.com/nicktill/tinyobs/pkg/httpx"
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
	Status string      `json:"status"`
	Data   *ResultData `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
	Query  string      `json:"query"` // Echo back the query
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

// HandleQueryExecute handles the /v1/query/execute endpoint.
func (h *Handler) HandleQueryExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	// Validate query
	if req.Query == "" {
		httpx.RespondErrorString(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Set defaults
	now := time.Now()
	if req.Start.IsZero() {
		req.Start = now.Add(-config.QueryDefaultWindow)
	}
	if req.End.IsZero() {
		req.End = now
	}

	// Parse step duration
	step := config.QueryDefaultStep
	if req.Step != "" {
		parsedStep, err := time.ParseDuration(req.Step)
		if err != nil {
			httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("invalid step duration: %w", err))
			return
		}
		step = parsedStep
	}

	// Validate time range
	if !req.Start.Before(req.End) {
		httpx.RespondErrorString(w, http.StatusBadRequest, "start must be before end")
		return
	}

	// Parse query
	parser := NewParser(req.Query)
	expr, err := parser.Parse()
	if err != nil {
		httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("query parse error: %w", err))
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
	ctx := r.Context()

	result, err := h.executor.Execute(ctx, query)
	if err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("query execution error: %w", err))
		return
	}
	// CRITICAL: Always close result to free memory
	defer result.Close()

	// Convert result to response format (copies data, so safe to close result after)
	response := QueryResponse{
		Status: "success",
		Query:  req.Query,
		Data: &ResultData{
			ResultType: "matrix",
			Result:     convertToSeriesResults(result),
		},
	}

	httpx.RespondJSON(w, http.StatusOK, response)
}

// HandleQueryInstant handles the /v1/query endpoint (instant queries).
// This is for compatibility with Prometheus query API.
func (h *Handler) HandleQueryInstant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Parse query parameters
	query := r.URL.Query().Get("query")
	if query == "" {
		httpx.RespondErrorString(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Parse time parameter (defaults to now)
	timeParam := r.URL.Query().Get("time")
	queryTime := time.Now()
	if timeParam != "" {
		if t, err := time.Parse(time.RFC3339, timeParam); err != nil {
			// Invalid time format - log and use default (now)
			log.Printf("Invalid time parameter %q, using current time: %v", timeParam, err)
		} else {
			queryTime = t
		}
	}

	// For instant queries, we need a small time window to find the latest values
	// Counters are stored with timestamps, so we look back 5 minutes to ensure we get data
	// The aggregation will take the most recent value from each series
	startTime := queryTime.Add(-5 * time.Minute)
	endTime := queryTime

	req := QueryRequest{
		Query: query,
		Start: startTime,
		End:   endTime,
		Step:  "1s", // Minimal step for instant query
	}

	// Parse query
	parser := NewParser(req.Query)
	expr, err := parser.Parse()
	if err != nil {
		httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("query parse error: %w", err))
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
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("query execution error: %w", err))
		return
	}
	// CRITICAL: Always close result to free memory
	defer result.Close()

	// Convert to instant query format (single values, not ranges)
	response := QueryResponse{
		Status: "success",
		Query:  req.Query,
		Data: &ResultData{
			ResultType: "vector",
			Result:     convertToInstantResults(result),
		},
	}

	httpx.RespondJSON(w, http.StatusOK, response)
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

// HandlePrometheusQuery handles GET /api/v1/query (Prometheus-compatible instant query).
// Maps Prometheus query parameters to TinyObs format and delegates to HandleQueryInstant.
func (h *Handler) HandlePrometheusQuery(w http.ResponseWriter, r *http.Request) {
	h.HandleQueryInstant(w, r)
}

// HandlePrometheusQueryRange handles GET /api/v1/query_range (Prometheus-compatible range query).
// Maps Prometheus query parameters to TinyObs format and delegates to HandleQueryExecute.
func (h *Handler) HandlePrometheusQueryRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	query := r.URL.Query()
	queryStr := query.Get("query")
	if queryStr == "" {
		httpx.RespondErrorString(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Parse Prometheus time parameters (Unix timestamps)
	now := time.Now()
	start := parsePrometheusTime(query.Get("start"), now.Add(-config.QueryDefaultWindow))
	end := parsePrometheusTime(query.Get("end"), now)
	step := parsePrometheusDuration(query.Get("step"), config.QueryDefaultStep)

	// Build query object and execute directly
	parser := NewParser(queryStr)
	expr, err := parser.Parse()
	if err != nil {
		httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("query parse error: %w", err))
		return
	}

	queryObj := &Query{
		Expr:  expr,
		Start: start,
		End:   end,
		Step:  step,
	}

	ctx := r.Context()
	result, err := h.executor.Execute(ctx, queryObj)
	if err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("query execution error: %w", err))
		return
	}
	defer result.Close()

	response := QueryResponse{
		Status: "success",
		Query:  queryStr,
		Data: &ResultData{
			ResultType: "matrix",
			Result:     convertToSeriesResults(result),
		},
	}

	httpx.RespondJSON(w, http.StatusOK, response)
}

// parsePrometheusTime parses Prometheus time parameter (Unix timestamp or RFC3339).
func parsePrometheusTime(param string, defaultTime time.Time) time.Time {
	if param == "" {
		return defaultTime
	}

	// Try Unix timestamp first (Prometheus default - float64 seconds since epoch)
	if unix, err := strconv.ParseFloat(param, 64); err == nil {
		sec := int64(unix)
		nsec := int64((unix - float64(sec)) * 1e9)
		return time.Unix(sec, nsec)
	}

	// Try RFC3339
	if t, err := time.Parse(time.RFC3339, param); err == nil {
		return t
	}

	return defaultTime
}

// parsePrometheusDuration parses Prometheus duration string (e.g., "15s", "1m").
func parsePrometheusDuration(param string, defaultDuration time.Duration) time.Duration {
	if param == "" {
		return defaultDuration
	}

	if d, err := time.ParseDuration(param); err == nil {
		return d
	}

	return defaultDuration
}
