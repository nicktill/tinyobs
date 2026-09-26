// Package sdk instruments Go applications and exports their metrics to
// TinyObs, or to anything else that accepts OTLP/HTTP.
//
// Instruments aggregate in memory. Every FlushEvery, the client sends their
// cumulative state as OTLP/JSON. Service becomes the job label and Instance
// the instance label.
//
// Applications already using the Prometheus client library or the
// OpenTelemetry SDK don't need this package: point TinyObs at their /metrics
// endpoint, or their OTLP exporter at TinyObs.
package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	goruntime "runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// ClientConfig configures a Client.
type ClientConfig struct {
	// Service names the application. Required. It becomes the job label.
	Service string
	// Instance identifies this process among replicas of the service. It
	// becomes the instance label. Default: the hostname.
	Instance string
	// Endpoint is the TinyObs base URL (the OTLP path is added) or a full
	// OTLP/HTTP metrics URL. Default: http://localhost:4318, the standard
	// OTLP/HTTP port, where TinyObs listens by default.
	Endpoint string
	// APIKey, if set, is sent as a bearer token.
	APIKey string
	// FlushEvery is the export interval. Default: 10s.
	FlushEvery time.Duration
}

// Client holds instruments and exports them.
type Client struct {
	cfg      ClientConfig
	url      string
	http     *http.Client
	start    time.Time
	instance string

	mu         sync.Mutex
	counters   map[string]*metrics.Counter
	gauges     map[string]*metrics.Gauge
	histograms map[string]*metrics.Histogram

	runMu   sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	lastErr time.Time // when a failure was last logged
}

// New creates a client. Call Start to begin exporting.
func New(cfg ClientConfig) (*Client, error) {
	if cfg.Service == "" {
		return nil, errors.New("service name is required")
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 10 * time.Second
	}
	endpoint, err := otlpURL(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	inst := cfg.Instance
	if inst == "" {
		inst, _ = os.Hostname()
	}
	return &Client{
		cfg:        cfg,
		url:        endpoint,
		http:       &http.Client{Timeout: 10 * time.Second},
		start:      time.Now(),
		instance:   inst,
		counters:   map[string]*metrics.Counter{},
		gauges:     map[string]*metrics.Gauge{},
		histograms: map[string]*metrics.Histogram{},
	}, nil
}

// otlpURL resolves the endpoint to the OTLP metrics URL.
func otlpURL(endpoint string) (string, error) {
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid endpoint %q", endpoint)
	}
	switch u.Path {
	case "", "/", "/v1/ingest": // base URL, or the V1 ingest URL
		u.Path = "/v1/metrics"
	}
	return u.String(), nil
}

// Counter returns the counter with the given name, creating it once.
func (c *Client) Counter(name string) metrics.CounterInterface {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.counters[name]; m != nil {
		return m
	}
	m := metrics.NewCounter(name)
	c.counters[name] = m
	return m
}

// Gauge returns the gauge with the given name, creating it once.
func (c *Client) Gauge(name string) metrics.GaugeInterface {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.gauges[name]; m != nil {
		return m
	}
	m := metrics.NewGauge(name)
	c.gauges[name] = m
	return m
}

// Histogram returns the histogram with the given name, creating it once with
// metrics.DefaultBuckets.
func (c *Client) Histogram(name string) metrics.HistogramInterface {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := c.histograms[name]; m != nil {
		return m
	}
	m := metrics.NewHistogram(name, nil)
	c.histograms[name] = m
	return m
}

// Start begins exporting every FlushEvery until Stop is called or ctx ends.
func (c *Client) Start(ctx context.Context) error {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if c.cancel != nil {
		return errors.New("client already started")
	}
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		t := time.NewTicker(c.cfg.FlushEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.report(c.Flush(context.Background()))
			}
		}
	}()
	return nil
}

// Stop stops periodic export and sends a final export, so values recorded
// just before shutdown are not lost.
func (c *Client) Stop() error {
	c.runMu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel = nil
	c.runMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	return c.Flush(ctx)
}

func (c *Client) report(err error) {
	if err == nil {
		return
	}
	// Don't flood the application's log while TinyObs is unreachable.
	if time.Since(c.lastErr) > time.Minute {
		log.Printf("tinyobs: exporting metrics: %v", err)
		c.lastErr = time.Now()
	}
}

// Flush exports the current state of every instrument now.
func (c *Client) Flush(ctx context.Context) error {
	body, err := json.Marshal(c.request(time.Now()))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(respBody))
	}
	var partial struct {
		PartialSuccess struct {
			Rejected     string `json:"rejectedDataPoints"`
			ErrorMessage string `json:"errorMessage"`
		} `json:"partialSuccess"`
	}
	if json.Unmarshal(respBody, &partial) == nil && partial.PartialSuccess.Rejected != "" && partial.PartialSuccess.Rejected != "0" {
		return fmt.Errorf("%s data points rejected: %s", partial.PartialSuccess.Rejected, partial.PartialSuccess.ErrorMessage)
	}
	return nil
}

// OTLP/JSON request, as defined by opentelemetry-proto's JSON mapping.

type otlpKV struct {
	Key   string `json:"key"`
	Value struct {
		StringValue string `json:"stringValue"`
	} `json:"value"`
}

func kv(k, v string) otlpKV {
	x := otlpKV{Key: k}
	x.Value.StringValue = v
	return x
}

type otlpPoint struct {
	Attributes        []otlpKV  `json:"attributes,omitempty"`
	StartTimeUnixNano string    `json:"startTimeUnixNano"`
	TimeUnixNano      string    `json:"timeUnixNano"`
	AsDouble          *float64  `json:"asDouble,omitempty"`
	Count             string    `json:"count,omitempty"`
	Sum               *float64  `json:"sum,omitempty"`
	BucketCounts      []string  `json:"bucketCounts,omitempty"`
	ExplicitBounds    []float64 `json:"explicitBounds,omitempty"`
}

type otlpData struct {
	AggregationTemporality int         `json:"aggregationTemporality,omitempty"`
	IsMonotonic            bool        `json:"isMonotonic,omitempty"`
	DataPoints             []otlpPoint `json:"dataPoints"`
}

type otlpMetric struct {
	Name      string    `json:"name"`
	Unit      string    `json:"unit,omitempty"`
	Sum       *otlpData `json:"sum,omitempty"`
	Gauge     *otlpData `json:"gauge,omitempty"`
	Histogram *otlpData `json:"histogram,omitempty"`
}

const cumulative = 2

func (c *Client) request(now time.Time) any {
	startNs := strconv.FormatInt(c.start.UnixNano(), 10)
	nowNs := strconv.FormatInt(now.UnixNano(), 10)
	point := func(p metrics.Point) otlpPoint {
		op := otlpPoint{StartTimeUnixNano: startNs, TimeUnixNano: nowNs}
		for _, l := range p.Labels {
			op.Attributes = append(op.Attributes, kv(l.Name, l.Value))
		}
		return op
	}
	numbers := func(ps []metrics.Point) []otlpPoint {
		out := make([]otlpPoint, 0, len(ps))
		for _, p := range ps {
			op := point(p)
			v := p.Value
			op.AsDouble = &v
			out = append(out, op)
		}
		return out
	}

	c.collectRuntime()
	c.mu.Lock()
	defer c.mu.Unlock()
	var ms []otlpMetric
	for _, name := range sortedKeys(c.counters) {
		if ps := c.counters[name].Snapshot(); len(ps) > 0 {
			ms = append(ms, otlpMetric{Name: name, Sum: &otlpData{AggregationTemporality: cumulative, IsMonotonic: true, DataPoints: numbers(ps)}})
		}
	}
	for _, name := range sortedKeys(c.gauges) {
		if ps := c.gauges[name].Snapshot(); len(ps) > 0 {
			ms = append(ms, otlpMetric{Name: name, Gauge: &otlpData{DataPoints: numbers(ps)}})
		}
	}
	for _, name := range sortedKeys(c.histograms) {
		h := c.histograms[name]
		ps := h.Snapshot()
		if len(ps) == 0 {
			continue
		}
		var dps []otlpPoint
		for _, p := range ps {
			op := point(p)
			op.Count = strconv.FormatUint(p.Count, 10)
			sum := p.Sum
			op.Sum = &sum
			for _, n := range p.BucketCounts {
				op.BucketCounts = append(op.BucketCounts, strconv.FormatUint(n, 10))
			}
			op.ExplicitBounds = h.Bounds()
			dps = append(dps, op)
		}
		ms = append(ms, otlpMetric{Name: name, Histogram: &otlpData{AggregationTemporality: cumulative, DataPoints: dps}})
	}

	resource := []otlpKV{kv("service.name", c.cfg.Service)}
	if c.instance != "" {
		resource = append(resource, kv("service.instance.id", c.instance))
	}
	return map[string]any{
		"resourceMetrics": []any{map[string]any{
			"resource": map[string]any{"attributes": resource},
			"scopeMetrics": []any{map[string]any{
				"scope":   map[string]string{"name": "github.com/nicktill/tinyobs/pkg/sdk"},
				"metrics": ms,
			}},
		}},
	}
}

// collectRuntime updates the Go runtime gauges before each export.
func (c *Client) collectRuntime() {
	var m goruntime.MemStats
	goruntime.ReadMemStats(&m)
	c.Gauge("go_goroutines").Set(float64(goruntime.NumGoroutine()))
	c.Gauge("go_memstats_heap_alloc_bytes").Set(float64(m.HeapAlloc))
	c.Gauge("go_memstats_sys_bytes").Set(float64(m.Sys))
	c.Gauge("go_gc_cycles_total").Set(float64(m.NumGC))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
