// Package metrics implements the SDK's instruments. They aggregate in memory;
// the client exports their cumulative state on an interval.
package metrics

import (
	"sort"
	"strings"
	"sync"
)

// CounterInterface is a monotonically increasing value.
type CounterInterface interface {
	Inc(labels ...string)
	Add(value float64, labels ...string)
}

// GaugeInterface is a value that can go up and down.
type GaugeInterface interface {
	Set(value float64, labels ...string)
	Inc(labels ...string)
	Dec(labels ...string)
	Add(value float64, labels ...string)
	Sub(value float64, labels ...string)
}

// HistogramInterface records a distribution of observations in buckets.
type HistogramInterface interface {
	Observe(value float64, labels ...string)
}

// DefaultBuckets are histogram bucket upper bounds in seconds, suited to
// request latencies from 1ms to 10s. Values above the last bound fall in the
// implicit +Inf bucket.
var DefaultBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Label is one name/value pair of a series.
type Label struct{ Name, Value string }

// Point is the current state of one label combination.
type Point struct {
	Labels []Label // sorted by name
	Value  float64 // counters and gauges
	// Histograms: observations per bucket (len(Bounds)+1, the last being
	// +Inf), their total count and sum.
	BucketCounts []uint64
	Count        uint64
	Sum          float64
}

// labelsKey turns alternating name/value arguments into a map key and a
// sorted label list. A dangling name without a value is ignored.
func labelsKey(kv []string) (string, []Label) {
	ls := make([]Label, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		ls = append(ls, Label{kv[i], kv[i+1]})
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte(0xff)
		b.WriteString(l.Value)
		b.WriteByte(0xff)
	}
	return b.String(), ls
}

type value struct {
	labels []Label
	v      float64
}

// Counter is a cumulative, monotonically increasing value per label set.
type Counter struct {
	name   string
	mu     sync.Mutex
	series map[string]*value
}

// NewCounter returns a counter.
func NewCounter(name string) *Counter { return &Counter{name: name, series: map[string]*value{}} }

// Name returns the counter's name.
func (c *Counter) Name() string { return c.name }

// Inc adds 1.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Add adds v. Negative values are ignored: counters only increase.
func (c *Counter) Add(v float64, labels ...string) {
	if v < 0 {
		return
	}
	k, ls := labelsKey(labels)
	c.mu.Lock()
	s := c.series[k]
	if s == nil {
		s = &value{labels: ls}
		c.series[k] = s
	}
	s.v += v
	c.mu.Unlock()
}

// Snapshot returns the current value of every label set.
func (c *Counter) Snapshot() []Point { return snapshotValues(&c.mu, c.series) }

// Gauge is a value per label set that can go up and down.
type Gauge struct {
	name   string
	mu     sync.Mutex
	series map[string]*value
}

// NewGauge returns a gauge.
func NewGauge(name string) *Gauge { return &Gauge{name: name, series: map[string]*value{}} }

// Name returns the gauge's name.
func (g *Gauge) Name() string { return g.name }

func (g *Gauge) update(labels []string, f func(float64) float64) {
	k, ls := labelsKey(labels)
	g.mu.Lock()
	s := g.series[k]
	if s == nil {
		s = &value{labels: ls}
		g.series[k] = s
	}
	s.v = f(s.v)
	g.mu.Unlock()
}

func (g *Gauge) Set(v float64, labels ...string) {
	g.update(labels, func(float64) float64 { return v })
}
func (g *Gauge) Inc(labels ...string)            { g.Add(1, labels...) }
func (g *Gauge) Dec(labels ...string)            { g.Add(-1, labels...) }
func (g *Gauge) Sub(v float64, labels ...string) { g.Add(-v, labels...) }
func (g *Gauge) Add(v float64, labels ...string) {
	g.update(labels, func(old float64) float64 { return old + v })
}

// Snapshot returns the current value of every label set.
func (g *Gauge) Snapshot() []Point { return snapshotValues(&g.mu, g.series) }

func snapshotValues(mu *sync.Mutex, series map[string]*value) []Point {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Point, 0, len(series))
	for _, s := range series {
		out = append(out, Point{Labels: s.labels, Value: s.v})
	}
	return out
}

// Histogram counts observations in buckets per label set. Counts are
// cumulative over the life of the process, like Prometheus client histograms.
type Histogram struct {
	name   string
	bounds []float64
	mu     sync.Mutex
	series map[string]*histSeries
}

type histSeries struct {
	labels []Label
	counts []uint64
	count  uint64
	sum    float64
}

// NewHistogram returns a histogram with the given bucket upper bounds, which
// must be sorted. Nil means DefaultBuckets.
func NewHistogram(name string, bounds []float64) *Histogram {
	if bounds == nil {
		bounds = DefaultBuckets
	}
	return &Histogram{name: name, bounds: bounds, series: map[string]*histSeries{}}
}

// Name returns the histogram's name.
func (h *Histogram) Name() string { return h.name }

// Bounds returns the bucket upper bounds, excluding +Inf.
func (h *Histogram) Bounds() []float64 { return h.bounds }

// Observe records one value.
func (h *Histogram) Observe(v float64, labels ...string) {
	i := sort.SearchFloat64s(h.bounds, v) // first bound >= v; len(bounds) is +Inf
	k, ls := labelsKey(labels)
	h.mu.Lock()
	s := h.series[k]
	if s == nil {
		s = &histSeries{labels: ls, counts: make([]uint64, len(h.bounds)+1)}
		h.series[k] = s
	}
	s.counts[i]++
	s.count++
	s.sum += v
	h.mu.Unlock()
}

// Snapshot returns the current state of every label set.
func (h *Histogram) Snapshot() []Point {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Point, 0, len(h.series))
	for _, s := range h.series {
		out = append(out, Point{
			Labels:       s.labels,
			BucketCounts: append([]uint64(nil), s.counts...),
			Count:        s.count,
			Sum:          s.sum,
		})
	}
	return out
}
