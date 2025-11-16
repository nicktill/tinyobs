package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/batch"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/sdk/runtime"
	"github.com/nicktill/tinyobs/pkg/sdk/transport"
)

// ClientConfig holds configuration for the TinyObs client
type ClientConfig struct {
	Service   string        `json:"service"`
	APIKey    string        `json:"api_key"`
	Endpoint  string        `json:"endpoint"`
	FlushEvery time.Duration `json:"flush_every"`
}

// Client is the main TinyObs SDK client
type Client struct {
	config     ClientConfig
	transport  transport.Transport
	batcher    *batch.Batcher
	collectors []metrics.MetricCollector
	
	// Metric storage
	counters   map[string]*metrics.Counter
	gauges     map[string]*metrics.Gauge
	histograms map[string]*metrics.Histogram
	mu         sync.RWMutex
	
	// Runtime tracking
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
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

	// Add runtime collector
	runtimeCollector := runtime.NewCollector(cfg.Service)
	client.collectors = append(client.collectors, runtimeCollector)

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
	if c.started {
		return fmt.Errorf("client already started")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)
	c.started = true

	// Start the batcher
	if err := c.batcher.Start(c.ctx); err != nil {
		return fmt.Errorf("failed to start batcher: %w", err)
	}

	// Start runtime collection
	go c.collectRuntimeMetrics()

	return nil
}

// Stop stops the client and flushes remaining metrics
func (c *Client) Stop() error {
	if !c.started {
		return nil
	}

	if c.cancel != nil {
		c.cancel()
	}

	// Flush remaining metrics
	if err := c.batcher.Flush(); err != nil {
		return fmt.Errorf("failed to flush metrics: %w", err)
	}

	c.started = false
	return nil
}

// SendMetric sends a metric to the batcher (implements metrics.ClientInterface)
func (c *Client) SendMetric(metric metrics.Metric) {
	if !c.started {
		return
	}

	// Add service label
	if metric.Labels == nil {
		metric.Labels = make(map[string]string)
	}
	metric.Labels["service"] = c.config.Service

	c.batcher.Add(metric)
}

// collectRuntimeMetrics periodically collects runtime metrics
func (c *Client) collectRuntimeMetrics() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			for _, collector := range c.collectors {
				metrics := collector.Collect(c.ctx)
				for _, metric := range metrics {
					c.SendMetric(metric)
				}
			}
		}
	}
}
