package metrics

import (
	"sync"
	"time"
)

// Counter is a cumulative value that only goes up.
type Counter struct {
	name   string
	mu     sync.Mutex
	series seriesSet[float64]
}

// NewCounter creates a counter.
func NewCounter(name string) *Counter {
	return &Counter{name: name, series: newSeriesSet[float64]()}
}

// Inc increments the counter by 1.
func (c *Counter) Inc(labels ...string) { c.Add(1, labels...) }

// Add adds a non-negative value. Negative values are ignored, since a
// decreasing counter would read as a reset.
func (c *Counter) Add(value float64, labels ...string) {
	if value < 0 {
		return
	}
	c.mu.Lock()
	c.series.get(labels, zero).v += value
	c.mu.Unlock()
}

// Collect returns the current total of each series.
func (c *Counter) Collect(ts time.Time) []Metric {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Metric, 0, len(c.series.m))
	for _, e := range c.series.m {
		out = append(out, Metric{Name: c.name, Type: CounterType, Value: e.v, Labels: copyLabels(e.labels), Timestamp: ts})
	}
	return out
}

func zero() float64 { return 0 }
