package ingest

import (
	"fmt"
	"sync"

	"tinyobs/pkg/sdk/metrics"
)

// CardinalityTracker tracks unique time series to enforce cardinality limits
type CardinalityTracker struct {
	mu sync.RWMutex

	// seriesCount tracks unique series per metric name
	// metric_name -> count
	seriesCount map[string]int

	// totalSeries tracks total unique series across all metrics
	totalSeries int

	// seriesSeen tracks which series we've already counted
	// seriesKey(name, labels) -> true
	seriesSeen map[string]bool
}

// NewCardinalityTracker creates a new cardinality tracker
func NewCardinalityTracker() *CardinalityTracker {
	return &CardinalityTracker{
		seriesCount: make(map[string]int),
		seriesSeen:  make(map[string]bool),
	}
}

// Check validates that adding this metric won't exceed cardinality limits
// Returns error if limits would be exceeded
func (c *CardinalityTracker) Check(m metrics.Metric) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Create series key (metric name + sorted labels)
	key := seriesKey(m.Name, m.Labels)

	// If we've seen this series before, it's fine
	if c.seriesSeen[key] {
		return nil
	}

	// Check total series limit
	if c.totalSeries >= MaxUniqueSeries {
		return ErrCardinalityLimit
	}

	// Check per-metric cardinality limit
	if c.seriesCount[m.Name] >= MaxSeriesPerMetric {
		return ErrMetricCardinalityLimit
	}

	return nil
}

// Record marks a metric as seen, updating cardinality counters
// Should be called after Check() passes and metric is successfully written
func (c *CardinalityTracker) Record(m metrics.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := seriesKey(m.Name, m.Labels)

	// Only increment if this is a new series
	if !c.seriesSeen[key] {
		c.seriesSeen[key] = true
		c.seriesCount[m.Name]++
		c.totalSeries++
	}
}

// Stats returns current cardinality statistics
func (c *CardinalityTracker) Stats() CardinalityStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Find metric with highest cardinality
	var maxMetric string
	var maxCount int
	for name, count := range c.seriesCount {
		if count > maxCount {
			maxCount = count
			maxMetric = name
		}
	}

	return CardinalityStats{
		TotalSeries:      c.totalSeries,
		UniqueMetrics:    len(c.seriesCount),
		MaxSeriesMetric:  maxMetric,
		MaxSeriesCount:   maxCount,
		SeriesLimit:      MaxUniqueSeries,
		PerMetricLimit:   MaxSeriesPerMetric,
		UtilizationPct:   float64(c.totalSeries) / float64(MaxUniqueSeries) * 100,
	}
}

// CardinalityStats provides cardinality usage information
type CardinalityStats struct {
	TotalSeries      int     `json:"total_series"`
	UniqueMetrics    int     `json:"unique_metrics"`
	MaxSeriesMetric  string  `json:"max_series_metric"`
	MaxSeriesCount   int     `json:"max_series_count"`
	SeriesLimit      int     `json:"series_limit"`
	PerMetricLimit   int     `json:"per_metric_limit"`
	UtilizationPct   float64 `json:"utilization_percent"`
}

// seriesKey creates a unique key for a time series (metric name + labels)
func seriesKey(name string, labels map[string]string) string {
	// Import sort to make keys deterministic
	if len(labels) == 0 {
		return name
	}

	// Create sorted key
	// This is duplicated from storage packages, but keeps ingest independent
	key := name

	// Sort label keys for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		// Skip internal labels (like __resolution__)
		if len(k) > 0 && k[0] == '_' {
			continue
		}
		keys = append(keys, k)
	}

	// Simple bubble sort (fine for small arrays)
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	for _, k := range keys {
		key += fmt.Sprintf(",%s=%s", k, labels[k])
	}

	return key
}
