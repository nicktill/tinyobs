package metrics

import (
	"testing"
	"time"
)

func byLabel(ms []Metric, name, label string) map[string]float64 {
	out := map[string]float64{}
	for _, m := range ms {
		if m.Name == name {
			out[m.Labels[label]] = m.Value
		}
	}
	return out
}

func TestHistogramIsCumulative(t *testing.T) {
	h := NewHistogramWithBuckets("latency", []float64{0.1, 1, 10})
	for _, v := range []float64{0.05, 0.5, 5, 30} { // 30 is above every bound
		h.Observe(v)
	}
	first := h.Collect(time.Now())
	want := map[string]float64{"0.1": 1, "1": 2, "10": 3, "+Inf": 4}
	if got := byLabel(first, "latency_bucket", "le"); !equal(got, want) {
		t.Fatalf("buckets = %v, want %v", got, want)
	}
	if got := byLabel(first, "latency_count", "le")[""]; got != 4 {
		t.Fatalf("count = %v", got)
	}

	// Collecting must not reset: the series are counters, and a drop would
	// read as a counter reset to rate().
	h.Observe(0.05)
	want["0.1"], want["1"], want["10"], want["+Inf"] = 2, 3, 4, 5
	if got := byLabel(h.Collect(time.Now()), "latency_bucket", "le"); !equal(got, want) {
		t.Fatalf("after second collect: %v, want %v", got, want)
	}
}

func TestLabelValuesWithSeparators(t *testing.T) {
	c := NewCounter("hits")
	c.Inc("path", "/a,b", "method", "GET")
	c.Inc("path", "/a", "method", "b,GET")
	ms := c.Collect(time.Now())
	if len(ms) != 2 {
		t.Fatalf("got %d series, want 2: %v", len(ms), ms)
	}
	for _, m := range ms {
		if m.Value != 1 || len(m.Labels) != 2 {
			t.Fatalf("series %v", m)
		}
	}
}

func TestCounterAndGauge(t *testing.T) {
	c := NewCounter("c")
	c.Add(2)
	c.Add(-5) // ignored
	c.Inc()
	g := NewGauge("g")
	g.Set(10)
	g.Dec()
	g.Sub(4)
	if v := c.Collect(time.Now())[0].Value; v != 3 {
		t.Fatalf("counter = %v", v)
	}
	if v := g.Collect(time.Now())[0].Value; v != 5 {
		t.Fatalf("gauge = %v", v)
	}
}

func equal(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
