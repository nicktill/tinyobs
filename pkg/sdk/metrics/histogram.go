package metrics

import (
	"math"
	"sort"
	"strconv"
	"sync"
	"time"
)

// DefaultBuckets are upper bounds in seconds suited to HTTP request
// latencies, from 1ms to 10s. Every histogram also has a +Inf bucket.
var DefaultBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Histogram counts observations into cumulative buckets, exposed like a
// Prometheus classic histogram: <name>_bucket{le="..."}, <name>_sum and
// <name>_count. All three are counters, so query them with rate() and
// histogram_quantile(), e.g.
//
//	histogram_quantile(0.99, sum by (le) (rate(<name>_bucket[5m])))
type Histogram struct {
	name   string
	bounds []float64 // sorted, without +Inf
	les    []string  // formatted bounds, plus "+Inf"
	mu     sync.Mutex
	series seriesSet[*buckets]
}

type buckets struct {
	counts []uint64 // non-cumulative, one per bound plus +Inf
	sum    float64
	count  uint64
}

// NewHistogram creates a histogram with DefaultBuckets.
func NewHistogram(name string) *Histogram { return NewHistogramWithBuckets(name, DefaultBuckets) }

// NewHistogramWithBuckets creates a histogram with the given upper bounds.
// +Inf is added automatically.
func NewHistogramWithBuckets(name string, bounds []float64) *Histogram {
	b := make([]float64, 0, len(bounds))
	for _, x := range bounds {
		if !math.IsInf(x, 1) && !math.IsNaN(x) {
			b = append(b, x)
		}
	}
	sort.Float64s(b)
	les := make([]string, 0, len(b)+1)
	for _, x := range b {
		les = append(les, strconv.FormatFloat(x, 'g', -1, 64))
	}
	les = append(les, "+Inf")
	return &Histogram{name: name, bounds: b, les: les, series: newSeriesSet[*buckets]()}
}

// Observe records one value.
func (h *Histogram) Observe(value float64, labels ...string) {
	i := sort.SearchFloat64s(h.bounds, value) // first bound >= value; len(bounds) means +Inf
	h.mu.Lock()
	b := h.series.get(labels, func() *buckets { return &buckets{counts: make([]uint64, len(h.les))} }).v
	b.counts[i]++
	b.sum += value
	b.count++
	h.mu.Unlock()
}

// Collect returns the cumulative bucket counts, sum and count of each series.
func (h *Histogram) Collect(ts time.Time) []Metric {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Metric, 0, len(h.series.m)*(len(h.les)+2))
	for _, e := range h.series.m {
		var cum uint64
		for i, le := range h.les {
			cum += e.v.counts[i]
			out = append(out, Metric{Name: h.name + "_bucket", Type: HistogramType, Value: float64(cum), Labels: withLabel(e.labels, "le", le), Timestamp: ts})
		}
		out = append(out,
			Metric{Name: h.name + "_sum", Type: HistogramType, Value: e.v.sum, Labels: copyLabels(e.labels), Timestamp: ts},
			Metric{Name: h.name + "_count", Type: HistogramType, Value: float64(e.v.count), Labels: copyLabels(e.labels), Timestamp: ts},
		)
	}
	return out
}
