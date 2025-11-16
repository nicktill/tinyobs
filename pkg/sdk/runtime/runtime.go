package runtime

import (
	"context"
	"runtime"
	"time"

	"tinyobs/pkg/sdk/metrics"
)

// Collector collects runtime metrics
type Collector struct {
	service string
}

// NewCollector creates a new runtime collector
func NewCollector(service string) *Collector {
	return &Collector{
		service: service,
	}
}

// Collect collects runtime metrics
func (c *Collector) Collect(ctx context.Context) []metrics.Metric {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	metrics := []metrics.Metric{
		{
			Name:   "go_memstats_heap_alloc_bytes",
			Type:   metrics.GaugeType,
			Value:  float64(m.HeapAlloc),
			Labels: map[string]string{"service": c.service},
			Timestamp: time.Now(),
		},
		{
			Name:   "go_memstats_heap_sys_bytes",
			Type:   metrics.GaugeType,
			Value:  float64(m.HeapSys),
			Labels: map[string]string{"service": c.service},
			Timestamp: time.Now(),
		},
		{
			Name:   "go_memstats_heap_objects",
			Type:   metrics.GaugeType,
			Value:  float64(m.HeapObjects),
			Labels: map[string]string{"service": c.service},
			Timestamp: time.Now(),
		},
		{
			Name:   "go_memstats_gc_duration_seconds",
			Type:   metrics.CounterType,
			Value:  float64(m.PauseTotalNs) / 1e9,
			Labels: map[string]string{"service": c.service},
			Timestamp: time.Now(),
		},
		{
			Name:   "go_goroutines",
			Type:   metrics.GaugeType,
			Value:  float64(runtime.NumGoroutine()),
			Labels: map[string]string{"service": c.service},
			Timestamp: time.Now(),
		},
		{
			Name:   "go_gc_cycles_total",
			Type:   metrics.CounterType,
			Value:  float64(m.NumGC),
			Labels: map[string]string{"service": c.service},
			Timestamp: time.Now(),
		},
	}
	
	return metrics
}


