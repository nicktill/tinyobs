package compaction

import (
	"fmt"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// Resolution represents the granularity of aggregated data
type Resolution string

const (
	ResolutionRaw Resolution = "raw" // Original samples
	Resolution5m  Resolution = "5m"  // 5-minute aggregates
	Resolution1h  Resolution = "1h"  // 1-hour aggregates
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
// Stores aggregate metadata in special labels for proper re-aggregation
func (a *Aggregate) ToMetric() metrics.Metric {
	// Copy user labels and add aggregate metadata
	labels := make(map[string]string)
	for k, v := range a.Labels {
		labels[k] = v
	}

	// Store resolution and aggregate statistics as special labels
	// This prevents data loss during re-aggregation
	labels["__resolution__"] = string(a.Resolution)
	labels["__sum__"] = fmt.Sprintf("%f", a.Sum)
	labels["__count__"] = fmt.Sprintf("%d", a.Count)
	labels["__min__"] = fmt.Sprintf("%f", a.Min)
	labels["__max__"] = fmt.Sprintf("%f", a.Max)

	return metrics.Metric{
		Name:      a.Name,
		Type:      metrics.GaugeType,
		Value:     a.Average(), // Store average as the main value
		Labels:    labels,
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

// FromMetric reconstructs an Aggregate from a metric with aggregate metadata
// Returns nil if the metric is not an aggregate (no __resolution__ label)
func FromMetric(m metrics.Metric) *Aggregate {
	// Check if this is an aggregate
	resolution, isAggregate := m.Labels["__resolution__"]
	if !isAggregate {
		return nil
	}

	// Parse aggregate metadata
	var sum, min, max float64
	var count uint64

	// Return nil if any required metadata is malformed
	if _, err := fmt.Sscanf(m.Labels["__sum__"], "%f", &sum); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(m.Labels["__count__"], "%d", &count); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(m.Labels["__min__"], "%f", &min); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(m.Labels["__max__"], "%f", &max); err != nil {
		return nil
	}

	// Remove special labels to get user labels
	userLabels := make(map[string]string)
	for k, v := range m.Labels {
		if len(k) > 0 && k[0] != '_' { // Skip __resolution__, __sum__, etc.
			userLabels[k] = v
		}
	}

	return &Aggregate{
		Name:       m.Name,
		Labels:     userLabels,
		Timestamp:  m.Timestamp,
		Resolution: Resolution(resolution),
		Sum:        sum,
		Count:      count,
		Min:        min,
		Max:        max,
	}
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
