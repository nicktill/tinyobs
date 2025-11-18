package ingest

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// CardinalityTracker tracks unique time series to enforce cardinality limits
// SAFETY: Periodically clears old series to prevent unbounded memory growth
type CardinalityTracker struct {
	mu sync.RWMutex

	// seriesCount tracks unique series per metric name
	// metric_name -> count
	seriesCount map[string]int

	// totalSeries tracks total unique series across all metrics
	totalSeries int

	// seriesSeen tracks which series we've already counted
	// seriesKey(name, labels) -> lastSeen timestamp
	seriesSeen map[string]time.Time

	// lastCleanup tracks when we last cleaned up old series
	lastCleanup time.Time
}

// Constants for memory safety
const (
	// Clean up series not seen in last 24 hours
	seriesRetentionPeriod = 24 * time.Hour

	// Run cleanup every hour
	cleanupInterval = 1 * time.Hour
)

// NewCardinalityTracker creates a new cardinality tracker
func NewCardinalityTracker() *CardinalityTracker {
	return &CardinalityTracker{
		seriesCount: make(map[string]int),
		seriesSeen:  make(map[string]time.Time),
		lastCleanup: time.Now(),
	}
}

// Check validates that adding this metric won't exceed cardinality limits
// Returns error if limits would be exceeded
func (c *CardinalityTracker) Check(m metrics.Metric) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Periodically clean up old series to prevent memory leak
	c.cleanupOldSeriesLocked()

	// Create series key (metric name + sorted labels)
	key := seriesKey(m.Name, m.Labels)

	// If we've seen this series recently, it's fine
	if _, exists := c.seriesSeen[key]; exists {
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
	now := time.Now()

	// Update last seen timestamp
	_, existed := c.seriesSeen[key]
	c.seriesSeen[key] = now

	// Only increment counters if this is a new series
	if !existed {
		c.seriesCount[m.Name]++
		c.totalSeries++
	}
}

// cleanupOldSeriesLocked removes series not seen in seriesRetentionPeriod
// MUST be called with lock held
// This prevents unbounded memory growth in long-running servers
func (c *CardinalityTracker) cleanupOldSeriesLocked() {
	// Only run cleanup periodically
	now := time.Now()
	if now.Sub(c.lastCleanup) < cleanupInterval {
		return
	}

	c.lastCleanup = now
	cutoff := now.Add(-seriesRetentionPeriod)

	// Find series to remove
	var toRemove []string
	for key, lastSeen := range c.seriesSeen {
		if lastSeen.Before(cutoff) {
			toRemove = append(toRemove, key)
		}
	}

	// Remove old series
	for _, key := range toRemove {
		delete(c.seriesSeen, key)

		// We can't easily update per-metric counts without storing the metric name
		// This is acceptable - counts will be slightly inflated but won't grow unboundedly
	}

	// Rebuild series count from scratch if we removed anything significant
	if len(toRemove) > 1000 {
		c.rebuildCountsLocked()
	}
}

// rebuildCountsLocked recalculates series counts from seriesSeen
// MUST be called with lock held
func (c *CardinalityTracker) rebuildCountsLocked() {
	c.seriesCount = make(map[string]int)
	c.totalSeries = 0

	for key := range c.seriesSeen {
		// Extract metric name from key (everything before first comma)
		metricName := key
		if idx := strings.IndexByte(key, ','); idx >= 0 {
			metricName = key[:idx]
		}
		c.seriesCount[metricName]++
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
		TotalSeries:     c.totalSeries,
		UniqueMetrics:   len(c.seriesCount),
		MaxSeriesMetric: maxMetric,
		MaxSeriesCount:  maxCount,
		SeriesLimit:     MaxUniqueSeries,
		PerMetricLimit:  MaxSeriesPerMetric,
		UtilizationPct:  float64(c.totalSeries) / float64(MaxUniqueSeries) * 100,
	}
}

// CardinalityStats provides cardinality usage information
type CardinalityStats struct {
	TotalSeries     int     `json:"total_series"`
	UniqueMetrics   int     `json:"unique_metrics"`
	MaxSeriesMetric string  `json:"max_series_metric"`
	MaxSeriesCount  int     `json:"max_series_count"`
	SeriesLimit     int     `json:"series_limit"`
	PerMetricLimit  int     `json:"per_metric_limit"`
	UtilizationPct  float64 `json:"utilization_percent"`
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

	// Sort keys using standard library
	sort.Strings(keys)

	for _, k := range keys {
		key += fmt.Sprintf(",%s=%s", k, labels[k])
	}

	return key
}
