package ingest

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nicktill/tinyobs/pkg/httpx"
	"github.com/nicktill/tinyobs/pkg/storage"
)

const (
	// API timeouts
	ingestTimeout = 5 * time.Second
	queryTimeout  = 10 * time.Second
	statsTimeout  = 5 * time.Second
	listTimeout   = 5 * time.Second

	// Query defaults and limits
	defaultQueryWindow    = 1 * time.Hour
	defaultMaxPoints      = 1000
	maxPointsLimit        = 5000
	metricsListLimit      = 10000
	metricsListTimeWindow = 24 * time.Hour
	maxQueryWindow        = 90 * 24 * time.Hour // 90 days max
)

// MetricsListResponse returns available metric names
type MetricsListResponse struct {
	Metrics []string `json:"metrics"`
	Count   int      `json:"count"`
}

// RangeQueryResponse returns time-series data optimized for charting
type RangeQueryResponse struct {
	Data []SeriesData `json:"data"`
}

// SeriesData represents a single time series
type SeriesData struct {
	Metric     string            `json:"metric"`
	Labels     map[string]string `json:"labels,omitempty"`
	Points     []Point           `json:"points"`
	Resolution string            `json:"resolution"`
}

// Point represents a single data point
type Point struct {
	Timestamp int64   `json:"t"` // Unix timestamp in milliseconds
	Value     float64 `json:"v"`
}

// HandleMetricsList returns list of available metrics.
func (h *Handler) HandleMetricsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), listTimeout)
	defer cancel()

	// Query last 24h to find active metrics
	results, err := h.storage.Query(ctx, storage.QueryRequest{
		Start: time.Now().Add(-metricsListTimeWindow),
		End:   time.Now(),
		Limit: metricsListLimit,
	})
	if err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("query failed: %w", err))
		return
	}

	// Extract unique metric names (skip aggregates)
	metricSet := make(map[string]bool)
	for _, m := range results {
		// Skip aggregate metadata
		if m.Labels != nil && m.Labels["__resolution__"] != "" {
			continue
		}
		metricSet[m.Name] = true
	}

	// Convert to sorted slice
	metrics := make([]string, 0, len(metricSet))
	for name := range metricSet {
		metrics = append(metrics, name)
	}
	sort.Strings(metrics)

	response := MetricsListResponse{
		Metrics: metrics,
		Count:   len(metrics),
	}
	httpx.RespondJSON(w, http.StatusOK, response)
}

// HandleRangeQuery returns time-series data with smart downsampling.
func (h *Handler) HandleRangeQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	query := r.URL.Query()

	// Required parameter
	metricName := query.Get("metric")
	if metricName == "" {
		httpx.RespondErrorString(w, http.StatusBadRequest, "metric parameter required")
		return
	}

	// Basic validation: ensure metric name is reasonable
	if len(metricName) > 256 {
		httpx.RespondErrorString(w, http.StatusBadRequest, "metric name too long (max 256 chars)")
		return
	}

	// Parse time range
	start := parseTimeParam(query.Get("start"), time.Now().Add(-1*time.Hour))
	end := parseTimeParam(query.Get("end"), time.Now())

	// Validate time range
	if end.Before(start) {
		httpx.RespondErrorString(w, http.StatusBadRequest, "end must be after start")
		return
	}

	// Prevent queries that are too large
	queryWindow := end.Sub(start)
	if queryWindow > maxQueryWindow {
		httpx.RespondErrorString(w, http.StatusBadRequest, "query window too large (max 90 days)")
		return
	}

	// Parse max points (default: 1000 for performance)
	maxPoints := defaultMaxPoints
	if mp := query.Get("maxPoints"); mp != "" {
		parsed, err := strconv.Atoi(mp)
		if err != nil {
			httpx.RespondError(w, http.StatusBadRequest, fmt.Errorf("invalid maxPoints: %q is not an integer", mp))
			return
		}
		if parsed <= 0 || parsed > maxPointsLimit {
			httpx.RespondErrorString(w, http.StatusBadRequest, fmt.Sprintf("maxPoints must be between 1 and %d", maxPointsLimit))
			return
		}
		maxPoints = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	// Query metrics
	results, err := h.storage.Query(ctx, storage.QueryRequest{
		Start:       start,
		End:         end,
		MetricNames: []string{metricName},
	})
	if err != nil {
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("query failed: %w", err))
		return
	}

	// Group by series (metric + labels)
	seriesMap := make(map[string]*SeriesData)

	for _, m := range results {
		// Skip aggregate metadata in labels for grouping
		userLabels := make(map[string]string)
		isAggregate := false
		resolution := "raw"

		if m.Labels != nil {
			for k, v := range m.Labels {
				if k == "__resolution__" {
					resolution = v
					isAggregate = true
				} else if k[0] != '_' {
					userLabels[k] = v
				}
			}
		}

		// Create series key
		seriesKey := makeSeriesKey(metricName, userLabels)

		series, exists := seriesMap[seriesKey]
		if !exists {
			series = &SeriesData{
				Metric:     metricName,
				Labels:     userLabels,
				Points:     []Point{},
				Resolution: resolution,
			}
			seriesMap[seriesKey] = series
		}

		// Add point
		series.Points = append(series.Points, Point{
			Timestamp: m.Timestamp.UnixMilli(),
			Value:     m.Value,
		})

		// Track highest resolution
		if isAggregate && series.Resolution == "raw" {
			series.Resolution = resolution
		}
	}

	// Downsample if needed and sort points
	for _, series := range seriesMap {
		// Sort by timestamp
		sort.Slice(series.Points, func(i, j int) bool {
			return series.Points[i].Timestamp < series.Points[j].Timestamp
		})

		// Downsample if too many points
		if len(series.Points) > maxPoints {
			series.Points = downsamplePoints(series.Points, maxPoints)
		}
	}

	// Convert map to slice
	var response RangeQueryResponse
	for _, series := range seriesMap {
		response.Data = append(response.Data, *series)
	}

	w.Header().Set("Cache-Control", "no-cache")
	httpx.RespondJSON(w, http.StatusOK, response)
}

// makeSeriesKey creates a unique key for a series
func makeSeriesKey(metric string, labels map[string]string) string {
	if len(labels) == 0 {
		return metric
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	key := metric
	for _, k := range keys {
		key += "," + k + "=" + labels[k]
	}
	return key
}

// downsamplePoints reduces points using average bucketing
func downsamplePoints(points []Point, maxPoints int) []Point {
	if len(points) <= maxPoints {
		return points
	}

	bucketSize := len(points) / maxPoints
	if bucketSize < 1 {
		bucketSize = 1
	}

	downsampled := make([]Point, 0, maxPoints)

	for i := 0; i < len(points); i += bucketSize {
		end := i + bucketSize
		if end > len(points) {
			end = len(points)
		}

		// Average the bucket
		var sumValue float64
		var timestamp int64
		count := 0

		for j := i; j < end; j++ {
			sumValue += points[j].Value
			if j == i {
				timestamp = points[j].Timestamp
			}
			count++
		}

		if count > 0 {
			downsampled = append(downsampled, Point{
				Timestamp: timestamp,
				Value:     sumValue / float64(count),
			})
		}
	}

	return downsampled
}
