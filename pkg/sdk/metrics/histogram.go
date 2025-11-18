package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Default histogram buckets (in seconds) optimized for HTTP request latencies
// Covers 1ms to 10s range
var DefaultBuckets = []float64{
	0.001, // 1ms
	0.005, // 5ms
	0.01,  // 10ms
	0.025, // 25ms
	0.05,  // 50ms
	0.1,   // 100ms
	0.25,  // 250ms
	0.5,   // 500ms
	1.0,   // 1s
	2.5,   // 2.5s
	5.0,   // 5s
	10.0,  // 10s
}

// bucketSet tracks histogram observations for a specific label combination
type bucketSet struct {
	buckets []float64 // Bucket upper bounds
	counts  []uint64  // Observations in each bucket
	sum     float64   // Sum of all observed values
	count   uint64    // Total number of observations
}

func newBucketSet(buckets []float64) *bucketSet {
	return &bucketSet{
		buckets: buckets,
		counts:  make([]uint64, len(buckets)),
		sum:     0,
		count:   0,
	}
}

// observe adds a value to the appropriate bucket
func (bs *bucketSet) observe(value float64) {
	bs.count++
	bs.sum += value

	// Find the bucket this value belongs to
	for i, bound := range bs.buckets {
		if value <= bound {
			bs.counts[i]++
		}
	}
}

// reset clears all buckets (called after flush)
func (bs *bucketSet) reset() {
	for i := range bs.counts {
		bs.counts[i] = 0
	}
	bs.sum = 0
	bs.count = 0
}

// Histogram implements the Histogram interface with proper bucketing
type Histogram struct {
	name    string
	client  ClientInterface
	buckets []float64
	mu      sync.Mutex
	sets    map[string]*bucketSet // Per label combination
	values  map[string][]float64  // Keep raw values for percentile calculations
}

// NewHistogram creates a new histogram metric with default buckets
func NewHistogram(name string, client ClientInterface) *Histogram {
	return &Histogram{
		name:    name,
		client:  client,
		buckets: DefaultBuckets,
		sets:    make(map[string]*bucketSet),
		values:  make(map[string][]float64),
	}
}

// Observe records a value in the histogram
// This ONLY accumulates in memory - does NOT send to database immediately!
func (h *Histogram) Observe(value float64, labels ...string) {
	key := h.makeKey(labels...)

	h.mu.Lock()
	defer h.mu.Unlock()

	// Get or create bucket set for this label combination
	if h.sets[key] == nil {
		h.sets[key] = newBucketSet(h.buckets)
	}

	// Add to buckets
	h.sets[key].observe(value)

	// Also store raw value for percentile calculations
	h.values[key] = append(h.values[key], value)

	// IMPORTANT: Do NOT send metric here!
	// Metrics are sent in bulk during periodic flush by the client
}

// Flush sends aggregated bucket counts to the database and resets counters
// This is called by the client's periodic flush mechanism
func (h *Histogram) Flush() []Metric {
	h.mu.Lock()
	defer h.mu.Unlock()

	var metrics []Metric

	// For each label combination, send bucket counts
	for labelKey, bs := range h.sets {
		if bs.count == 0 {
			continue // Skip empty buckets
		}

		labels := h.keyToLabels(labelKey)

		// Send cumulative bucket counts (Prometheus-style)
		for i, bound := range bs.buckets {
			bucketLabels := copyLabels(labels)
			bucketLabels["le"] = formatBound(bound)

			metrics = append(metrics, Metric{
				Name:   h.name + "_bucket",
				Type:   HistogramType,
				Value:  float64(bs.counts[i]),
				Labels: bucketLabels,
			})
		}

		// Send sum
		metrics = append(metrics, Metric{
			Name:   h.name + "_sum",
			Type:   HistogramType,
			Value:  bs.sum,
			Labels: copyLabels(labels),
		})

		// Send count
		metrics = append(metrics, Metric{
			Name:   h.name + "_count",
			Type:   HistogramType,
			Value:  float64(bs.count),
			Labels: copyLabels(labels),
		})

		// Reset buckets after flush
		bs.reset()
	}

	// Clear raw values to prevent memory leak
	h.values = make(map[string][]float64)

	return metrics
}

// GetStats returns basic statistics for the histogram
func (h *Histogram) GetStats(labels ...string) (count int, sum, min, max, avg float64) {
	key := h.makeKey(labels...)

	h.mu.Lock()
	values := make([]float64, len(h.values[key]))
	copy(values, h.values[key])
	h.mu.Unlock()

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

	h.mu.Lock()
	values := make([]float64, len(h.values[key]))
	copy(values, h.values[key])
	h.mu.Unlock()

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

// keyToLabels converts a key back to label map
func (h *Histogram) keyToLabels(key string) map[string]string {
	if key == "" {
		return nil
	}

	labels := make(map[string]string)
	// Parse key like "endpoint,/api/users,method,GET"
	parts := splitKey(key)
	for i := 0; i < len(parts)-1; i += 2 {
		labels[parts[i]] = parts[i+1]
	}
	return labels
}

// Helper to split key by commas
func splitKey(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, ",")
}

// copyLabels creates a copy of a label map
func copyLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	copy := make(map[string]string, len(labels))
	for k, v := range labels {
		copy[k] = v
	}
	return copy
}

// formatBound formats a bucket bound as a string
func formatBound(bound float64) string {
	if bound == 10.0 {
		return "+Inf" // Prometheus convention for last bucket
	}
	// Format with appropriate precision, removing trailing zeros
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", bound), "0"), ".")
}
