package sdk

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/batch"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/sdk/runtime"
	"github.com/nicktill/tinyobs/pkg/sdk/transport"
)

// ClientConfig holds configuration for the TinyObs client
type ClientConfig struct {
	Service    string        `json:"service"`
	APIKey     string        `json:"api_key"`
	Endpoint   string        `json:"endpoint"`
	FlushEvery time.Duration `json:"flush_every"`
}

// Client is the main TinyObs SDK client
type Client struct {
	config    ClientConfig
	transport transport.Transport
	batcher   *batch.Batcher

	// Metric storage
	counters   map[string]*metrics.Counter
	gauges     map[string]*metrics.Gauge
	histograms map[string]*metrics.Histogram
	mu         sync.RWMutex

	// Runtime tracking
	started   atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc
	collector *runtime.Collector
}

// New creates a new TinyObs client
func New(cfg ClientConfig) (*Client, error) {
	if cfg.Service == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://localhost:8080/v1/ingest"
	}
	if cfg.FlushEvery == 0 {
		cfg.FlushEvery = 5 * time.Second
	}

	// Create transport
	trans, err := transport.NewHTTP(cfg.Endpoint, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	// Create batcher
	batcher := batch.New(trans, batch.Config{
		MaxBatchSize: 1000,
		FlushEvery:   cfg.FlushEvery,
	})

	client := &Client{
		config:     cfg,
		transport:  trans,
		batcher:    batcher,
		counters:   make(map[string]*metrics.Counter),
		gauges:     make(map[string]*metrics.Gauge),
		histograms: make(map[string]*metrics.Histogram),
	}

	return client, nil
}

// Counter returns a counter metric with the given name
func (c *Client) Counter(name string) metrics.CounterInterface {
	c.mu.Lock()
	defer c.mu.Unlock()

	if counter, exists := c.counters[name]; exists {
		return counter
	}

	counter := metrics.NewCounter(name, c)
	c.counters[name] = counter
	return counter
}

// Gauge returns a gauge metric with the given name
func (c *Client) Gauge(name string) metrics.GaugeInterface {
	c.mu.Lock()
	defer c.mu.Unlock()

	if gauge, exists := c.gauges[name]; exists {
		return gauge
	}

	gauge := metrics.NewGauge(name, c)
	c.gauges[name] = gauge
	return gauge
}

// Histogram returns a histogram metric with the given name
func (c *Client) Histogram(name string) metrics.HistogramInterface {
	c.mu.Lock()
	defer c.mu.Unlock()

	if histogram, exists := c.histograms[name]; exists {
		return histogram
	}

	histogram := metrics.NewHistogram(name, c)
	c.histograms[name] = histogram
	return histogram
}

// Start starts the client and begins collecting metrics
func (c *Client) Start(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return fmt.Errorf("client already started")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)

	// Start the batcher
	if err := c.batcher.Start(c.ctx); err != nil {
		c.started.Store(false)
		return fmt.Errorf("failed to start batcher: %w", err)
	}

	// Start histogram flushing (aggregates observations into buckets)
	go c.flushHistograms()

	// Start runtime metrics collection
	c.collector = runtime.NewCollector(c, 15*time.Second)
	go c.collector.Start(c.ctx)

	return nil
}

// Stop stops the client and flushes remaining metrics
func (c *Client) Stop() error {
	if !c.started.Load() {
		return nil
	}

	if c.cancel != nil {
		c.cancel()
	}

	// Flush remaining metrics
	if err := c.batcher.Flush(); err != nil {
		return fmt.Errorf("failed to flush metrics: %w", err)
	}

	c.started.Store(false)
	return nil
}

// SendMetric sends a metric to the batcher (implements metrics.ClientInterface)
func (c *Client) SendMetric(metric metrics.Metric) {
	if !c.started.Load() {
		return
	}

	// Add service label
	if metric.Labels == nil {
		metric.Labels = make(map[string]string)
	}
	metric.Labels["service"] = c.config.Service

	c.batcher.Add(metric)
}

// flushHistograms periodically flushes histogram buckets
// This sends aggregated bucket counts instead of individual observations
func (c *Client) flushHistograms() {
	// Flush interval should match the batch flush interval
	ticker := time.NewTicker(c.config.FlushEvery)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			histograms := make([]*metrics.Histogram, 0, len(c.histograms))
			for _, h := range c.histograms {
				histograms = append(histograms, h)
			}
			c.mu.RUnlock()

			// Flush each histogram and send aggregated metrics
			for _, h := range histograms {
				aggregatedMetrics := h.Flush()
				for _, metric := range aggregatedMetrics {
					c.SendMetric(metric)
				}
			}
		}
	}
}
