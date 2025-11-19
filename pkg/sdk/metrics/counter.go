package metrics

import (
	"sync"
	"time"
)

// Counter implements the Counter interface
type Counter struct {
	name   string
	client ClientInterface
	mu     sync.RWMutex
	values map[string]float64
}

// ClientInterface defines the interface for sending metrics
type ClientInterface interface {
	SendMetric(metric Metric)
}

// NewCounter creates a new counter metric
func NewCounter(name string, client ClientInterface) *Counter {
	return &Counter{
		name:   name,
		client: client,
		values: make(map[string]float64),
	}
}

// Inc increments the counter by 1
func (c *Counter) Inc(labels ...string) {
	c.Add(1, labels...)
}

// Add adds the given value to the counter
func (c *Counter) Add(value float64, labels ...string) {
	if value < 0 {
		return // Counters can only increase
	}

	key := c.makeKey(labels...)

	// CRITICAL FIX: Read value while holding lock to prevent race condition
	c.mu.Lock()
	c.values[key] += value
	newValue := c.values[key] // Capture value before releasing lock
	c.mu.Unlock()

	// Send metric immediately for real-time updates
	c.client.SendMetric(Metric{
		Name:      c.name,
		Type:      CounterType,
		Value:     newValue, // Use captured value (race-free)
		Labels:    c.makeLabels(labels...),
		Timestamp: time.Now(),
	})
}

// makeKey creates a key from labels for internal storage
func (c *Counter) makeKey(labels ...string) string {
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
func (c *Counter) makeLabels(labels ...string) map[string]string {
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
