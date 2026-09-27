package metrics

import (
	"sync"
	"time"
)

// Gauge is a value that can go up and down.
type Gauge struct {
	name   string
	mu     sync.Mutex
	series seriesSet[float64]
}

// NewGauge creates a gauge.
func NewGauge(name string) *Gauge {
	return &Gauge{name: name, series: newSeriesSet[float64]()}
}

// Set sets the gauge.
func (g *Gauge) Set(value float64, labels ...string) {
	g.mu.Lock()
	g.series.get(labels, zero).v = value
	g.mu.Unlock()
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc(labels ...string) { g.Add(1, labels...) }

// Dec decrements the gauge by 1.
func (g *Gauge) Dec(labels ...string) { g.Add(-1, labels...) }

// Sub subtracts value from the gauge.
func (g *Gauge) Sub(value float64, labels ...string) { g.Add(-value, labels...) }

// Add adds value (which may be negative) to the gauge.
func (g *Gauge) Add(value float64, labels ...string) {
	g.mu.Lock()
	g.series.get(labels, zero).v += value
	g.mu.Unlock()
}

// Collect returns the current value of each series.
func (g *Gauge) Collect(ts time.Time) []Metric {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Metric, 0, len(g.series.m))
	for _, e := range g.series.m {
		out = append(out, Metric{Name: g.name, Type: GaugeType, Value: e.v, Labels: copyLabels(e.labels), Timestamp: ts})
	}
	return out
}
