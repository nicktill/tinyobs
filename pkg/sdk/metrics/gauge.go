package metrics

import (
	"sync"
	"time"
)

// Gauge implements the Gauge interface
type Gauge struct {
	name   string
	client ClientInterface
	mu     sync.RWMutex
	values map[string]float64
}

// NewGauge creates a new gauge metric
func NewGauge(name string, client ClientInterface) *Gauge {
	return &Gauge{
		name:   name,
		client: client,
		values: make(map[string]float64),
	}
}

// Set sets the gauge to the given value
func (g *Gauge) Set(value float64, labels ...string) {
	key := g.makeKey(labels...)
	
	g.mu.Lock()
	g.values[key] = value
	g.mu.Unlock()

	// Send metric immediately for real-time updates
	g.client.SendMetric(Metric{
		Name:      g.name,
		Type:      GaugeType,
		Value:     value,
		Labels:    g.makeLabels(labels...),
		Timestamp: time.Now(),
	})
}

// Inc increments the gauge by 1
func (g *Gauge) Inc(labels ...string) {
	g.Add(1, labels...)
}

// Dec decrements the gauge by 1
func (g *Gauge) Dec(labels ...string) {
	g.Sub(1, labels...)
}

// Add adds the given value to the gauge
func (g *Gauge) Add(value float64, labels ...string) {
	key := g.makeKey(labels...)
	
	g.mu.Lock()
	g.values[key] += value
	g.mu.Unlock()

	// Send metric immediately for real-time updates
	g.client.SendMetric(Metric{
		Name:      g.name,
		Type:      GaugeType,
		Value:     g.values[key],
		Labels:    g.makeLabels(labels...),
		Timestamp: time.Now(),
	})
}

// Sub subtracts the given value from the gauge
func (g *Gauge) Sub(value float64, labels ...string) {
	key := g.makeKey(labels...)
	
	g.mu.Lock()
	g.values[key] -= value
	g.mu.Unlock()

	// Send metric immediately for real-time updates
	g.client.SendMetric(Metric{
		Name:      g.name,
		Type:      GaugeType,
		Value:     g.values[key],
		Labels:    g.makeLabels(labels...),
		Timestamp: time.Now(),
	})
}

// makeKey creates a key from labels for internal storage
func (g *Gauge) makeKey(labels ...string) string {
	if len(labels) == 0 {
		return ""
	}
	
	key := ""
	for i, label := range labels {
		if i > 0 {
			key += ","
		}
		key += label
	}
	return key
}

// makeLabels creates a label map from key-value pairs
func (g *Gauge) makeLabels(labels ...string) map[string]string {
	if len(labels)%2 != 0 {
		return nil
	}
	
	result := make(map[string]string)
	for i := 0; i < len(labels); i += 2 {
		if i+1 < len(labels) {
			result[labels[i]] = labels[i+1]
		}
	}
	return result
}