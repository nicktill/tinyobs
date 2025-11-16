package compaction

import (
	"time"

	"tinyobs/pkg/sdk/metrics"
)

// Resolution represents the granularity of aggregated data
type Resolution string

const (
	ResolutionRaw Resolution = "raw"   // Original samples
	Resolution5m  Resolution = "5m"    // 5-minute aggregates
	Resolution1h  Resolution = "1h"    // 1-hour aggregates
)

// Aggregate stores aggregated metrics for a time bucket
type Aggregate struct {
	// Metric identification
	Name   string
	Labels map[string]string

	// Time bucket
	Timestamp  time.Time
	Resolution Resolution

	// Aggregated values
	Sum   float64
	Count uint64
	Min   float64
	Max   float64

	// For percentile calculations (optional, expensive)
	Values []float64 // Only populated if needed
}

// ToMetric converts an aggregate back to a metric representation
// This allows aggregates to be stored using the same storage interface
func (a *Aggregate) ToMetric() metrics.Metric {
	return metrics.Metric{
		Name:      a.Name,
		Type:      metrics.GaugeType, // Aggregates are treated as gauges
		Value:     a.Average(),
		Labels:    a.Labels,
		Timestamp: a.Timestamp,
	}
}

// Average calculates the mean value
func (a *Aggregate) Average() float64 {
	if a.Count == 0 {
		return 0
	}
	return a.Sum / float64(a.Count)
}

// Percentile calculates the Pth percentile from stored values
// Only works if Values were populated during aggregation
func (a *Aggregate) Percentile(p float64) float64 {
	if len(a.Values) == 0 {
		return 0
	}

	// Simple percentile calculation
	// In production, would use more efficient algorithm
	index := int(p * float64(len(a.Values)-1))
	if index >= len(a.Values) {
		index = len(a.Values) - 1
	}

	return a.Values[index]
}
