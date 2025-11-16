package metrics

import (
	"context"
	"time"
)

// MetricType represents the type of metric
type MetricType string

const (
	CounterType   MetricType = "counter"
	GaugeType     MetricType = "gauge"
	HistogramType MetricType = "histogram"
)

// Metric represents a single metric data point
type Metric struct {
	Name      string            `json:"name"`
	Type      MetricType        `json:"type"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// CounterInterface represents a counter metric
type CounterInterface interface {
	Inc(labels ...string)
	Add(value float64, labels ...string)
}

// GaugeInterface represents a gauge metric
type GaugeInterface interface {
	Set(value float64, labels ...string)
	Inc(labels ...string)
	Dec(labels ...string)
	Add(value float64, labels ...string)
	Sub(value float64, labels ...string)
}

// HistogramInterface represents a histogram metric
type HistogramInterface interface {
	Observe(value float64, labels ...string)
}

// MetricCollector collects metrics and sends them to the client
type MetricCollector interface {
	Collect(ctx context.Context) []Metric
}
