package runtime

import (
	"context"
	"runtime"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// Collector automatically collects Go runtime metrics.
type Collector struct {
	client   metrics.ClientInterface
	interval time.Duration
}

// NewCollector creates a new runtime metrics collector.
func NewCollector(client metrics.ClientInterface, interval time.Duration) *Collector {
	if interval == 0 {
		interval = 15 * time.Second
	}
	return &Collector{
		client:   client,
		interval: interval,
	}
}

// Start begins collecting runtime metrics in the background.
func (c *Collector) Start(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Collect immediately on start
	c.collect()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// collect gathers and sends Go runtime metrics.
func (c *Collector) collect() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	now := time.Now()

	// Goroutines
	c.client.SendMetric(metrics.Metric{
		Name:      "go_goroutines",
		Type:      metrics.GaugeType,
		Value:     float64(runtime.NumGoroutine()),
		Timestamp: now,
	})

	// CPU count (constant, but useful for monitoring)
	c.client.SendMetric(metrics.Metric{
		Name:      "go_cpu_count",
		Type:      metrics.GaugeType,
		Value:     float64(runtime.NumCPU()),
		Timestamp: now,
	})

	// Memory metrics
	c.client.SendMetric(metrics.Metric{
		Name:      "go_memory_heap_bytes",
		Type:      metrics.GaugeType,
		Value:     float64(m.HeapAlloc),
		Timestamp: now,
	})

	c.client.SendMetric(metrics.Metric{
		Name:      "go_memory_stack_bytes",
		Type:      metrics.GaugeType,
		Value:     float64(m.StackInuse),
		Timestamp: now,
	})

	c.client.SendMetric(metrics.Metric{
		Name:      "go_memory_sys_bytes",
		Type:      metrics.GaugeType,
		Value:     float64(m.Sys),
		Timestamp: now,
	})

	// GC metrics
	c.client.SendMetric(metrics.Metric{
		Name:      "go_gc_count",
		Type:      metrics.CounterType,
		Value:     float64(m.NumGC),
		Timestamp: now,
	})

	// GC pause duration (last pause in seconds)
	if m.NumGC > 0 {
		// PauseTotalNs is the cumulative pause time, convert to seconds
		c.client.SendMetric(metrics.Metric{
			Name:      "go_gc_pause_seconds",
			Type:      metrics.CounterType,
			Value:     float64(m.PauseTotalNs) / 1e9, // Convert nanoseconds to seconds
			Timestamp: now,
		})
	}
}
