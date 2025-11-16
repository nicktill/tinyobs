package metrics

import (
	"sort"
	"sync"
	"time"
)

// Histogram implements the Histogram interface
type Histogram struct {
	name   string
	client ClientInterface
	mu     sync.RWMutex
	values map[string][]float64
}

// NewHistogram creates a new histogram metric
func NewHistogram(name string, client ClientInterface) *Histogram {
	return &Histogram{
		name:   name,
		client: client,
		values: make(map[string][]float64),
	}
}

// Observe records a value in the histogram
func (h *Histogram) Observe(value float64, labels ...string) {
	key := h.makeKey(labels...)
	
	h.mu.Lock()
	h.values[key] = append(h.values[key], value)
	h.mu.Unlock()

	// Send metric immediately for real-time updates
	h.client.SendMetric(Metric{
		Name:      h.name,
		Type:      HistogramType,
		Value:     value,
		Labels:    h.makeLabels(labels...),
		Timestamp: time.Now(),
	})
}

// GetStats returns basic statistics for the histogram
func (h *Histogram) GetStats(labels ...string) (count int, sum, min, max, avg float64) {
	key := h.makeKey(labels...)
	
	h.mu.RLock()
	values := make([]float64, len(h.values[key]))
	copy(values, h.values[key])
	h.mu.RUnlock()

	if len(values) == 0 {
		return 0, 0, 0, 0, 0
	}

	count = len(values)
	sum = 0
	min = values[0]
	max = values[0]

	for _, v := range values {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	avg = sum / float64(count)
	return count, sum, min, max, avg
}

// GetPercentile returns the value at the given percentile
func (h *Histogram) GetPercentile(percentile float64, labels ...string) float64 {
	key := h.makeKey(labels...)
	
	h.mu.RLock()
	values := make([]float64, len(h.values[key]))
	copy(values, h.values[key])
	h.mu.RUnlock()

	if len(values) == 0 {
		return 0
	}

	sort.Float64s(values)
	index := int(percentile * float64(len(values)-1))
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

// makeKey creates a key from labels for internal storage
func (h *Histogram) makeKey(labels ...string) string {
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
func (h *Histogram) makeLabels(labels ...string) map[string]string {
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