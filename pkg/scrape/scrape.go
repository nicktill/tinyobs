package scrape

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// MaxBodyBytes caps a scrape response.
const MaxBodyBytes = 16 << 20

// Target is an endpoint to scrape.
type Target struct {
	Job string
	URL *url.URL
}

// Instance is the target's instance label: host:port.
func (t Target) Instance() string { return t.URL.Host }

// ParseTarget parses "[job=]host:port[/path]" or "[job=]http(s)://host:port/path".
// The path defaults to /metrics and the job to the instance.
func ParseTarget(spec string) (Target, error) {
	job := ""
	if i := strings.Index(spec, "="); i >= 0 && !strings.Contains(spec[:i], "/") {
		job, spec = spec[:i], spec[i+1:]
		if !labels.IsValidMetricName(strings.ReplaceAll(job, "-", "_")) {
			return Target{}, fmt.Errorf("invalid job name %q", job)
		}
	}
	if !strings.Contains(spec, "://") {
		spec = "http://" + spec
	}
	u, err := url.Parse(spec)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return Target{}, fmt.Errorf("invalid scrape target %q", spec)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/metrics"
	}
	if job == "" {
		job = u.Host
	}
	return Target{Job: job, URL: u}, nil
}

// Status is a target's latest scrape result.
type Status struct {
	Target
	Health         string // "up", "down" or "unknown"
	LastError      string
	LastScrape     time.Time
	LastDuration   time.Duration
	Interval       time.Duration
	Timeout        time.Duration
	SamplesScraped int
}

// Manager scrapes a fixed set of targets.
type Manager struct {
	db       *tsdb.DB
	interval time.Duration
	timeout  time.Duration
	client   *http.Client
	log      *slog.Logger

	mu      sync.Mutex
	targets []*target
}

type target struct {
	Target
	status Status
	prev   map[string]labels.Labels // series from the last scrape, for staleness
}

// NewManager creates a manager. Timeout is capped at the interval.
func NewManager(db *tsdb.DB, targets []Target, interval, timeout time.Duration, log *slog.Logger) *Manager {
	if timeout <= 0 || timeout > interval {
		timeout = interval
	}
	if len(targets) > 0 {
		for name, md := range syntheticMetadata {
			db.SetMetadata(name, md)
		}
	}
	m := &Manager{db: db, interval: interval, timeout: timeout, log: log, client: &http.Client{}}
	for _, t := range targets {
		m.targets = append(m.targets, &target{
			Target: t,
			status: Status{Target: t, Health: "unknown", Interval: interval, Timeout: timeout},
			prev:   map[string]labels.Labels{},
		})
	}
	return m
}

// Run scrapes every target at the interval until ctx is done. Each target's
// start is offset by a hash of its URL so scrapes don't all fire at once.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range m.targets {
		wg.Add(1)
		go func(t *target) {
			defer wg.Done()
			h := fnv.New64a()
			h.Write([]byte(t.URL.String()))
			offset := time.Duration(h.Sum64() % uint64(m.interval))
			select {
			case <-ctx.Done():
				return
			case <-time.After(offset):
			}
			tick := time.NewTicker(m.interval)
			defer tick.Stop()
			for {
				m.scrape(ctx, t, time.Now())
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}(t)
	}
	wg.Wait()
}

// Status returns the state of every target.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, len(m.targets))
	for i, t := range m.targets {
		out[i] = t.status
	}
	return out
}

// ScrapeOnce scrapes every target once, now. For tests.
func (m *Manager) ScrapeOnce(ctx context.Context, now time.Time) {
	for _, t := range m.targets {
		m.scrape(ctx, t, now)
	}
}

func (m *Manager) scrape(ctx context.Context, t *target, now time.Time) {
	ts := now.UnixMilli()
	start := time.Now()
	res, err := m.fetch(ctx, t)
	duration := time.Since(start)
	if ctx.Err() != nil {
		return // shutting down; don't record a failure
	}

	targetLabels := func(ls labels.Labels) labels.Labels {
		// Exposed job/instance labels are kept as exported_*, as Prometheus
		// does with honor_labels: false.
		for _, name := range []string{"job", "instance"} {
			if v := ls.Get(name); v != "" {
				ls = ls.Set("exported_"+name, v)
			}
		}
		return ls.Set("job", t.Job).Set("instance", t.Instance())
	}

	app := m.db.Appender()
	cur := map[string]labels.Labels{}
	scraped := 0
	if err == nil {
		for _, s := range res.Samples {
			ls := targetLabels(s.Labels)
			st := s.T
			if st == 0 {
				st = ts
			}
			app.Append(ls, st, s.V)
			cur[ls.String()] = ls
		}
		scraped = len(res.Samples)
		m.storeMetadata(res)
	}
	// Series that disappeared get a staleness marker.
	for k, ls := range t.prev {
		if _, ok := cur[k]; !ok {
			app.Append(ls, ts, tsdb.StaleNaN)
		}
	}
	up := 0.0
	if err == nil {
		up = 1
	}
	base := labels.FromStrings("job", t.Job, "instance", t.Instance())
	app.Append(base.Set(labels.MetricName, "up"), ts, up)
	app.Append(base.Set(labels.MetricName, "scrape_duration_seconds"), ts, duration.Seconds())
	app.Append(base.Set(labels.MetricName, "scrape_samples_scraped"), ts, float64(scraped))
	cres, cerr := app.Commit()

	m.mu.Lock()
	t.prev = cur
	t.status.LastScrape = now
	t.status.LastDuration = duration
	t.status.SamplesScraped = scraped
	switch {
	case err != nil:
		t.status.Health, t.status.LastError = "down", err.Error()
	case cerr != nil:
		t.status.Health, t.status.LastError = "down", "storing samples: "+cerr.Error()
	default:
		t.status.Health, t.status.LastError = "up", ""
		if cres.NumRejected() > 0 {
			t.status.LastError = fmt.Sprintf("%d samples rejected: %v", cres.NumRejected(), cres.FirstError)
		}
	}
	m.mu.Unlock()

	if err != nil {
		m.log.Debug("scrape failed", "job", t.Job, "url", t.URL.String(), "err", err)
	}
}

func (m *Manager) fetch(ctx context.Context, t *target) (*ParseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/openmetrics-text;version=1.0.0;q=0.9,text/plain;version=0.0.4;q=0.5,*/*;q=0.1")
	req.Header.Set("User-Agent", "TinyObs")
	req.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", fmt.Sprintf("%g", m.timeout.Seconds()))
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBodyBytes {
		return nil, errors.New("response exceeds 16 MiB")
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return Parse(body, mt == "application/openmetrics-text")
}

// syntheticMetadata describes the series every scrape adds.
var syntheticMetadata = map[string]tsdb.Metadata{
	"up":                      {Type: "gauge", Help: "1 if the last scrape of the target succeeded, 0 if it failed."},
	"scrape_duration_seconds": {Type: "gauge", Help: "How long the last scrape of the target took.", Unit: "seconds"},
	"scrape_samples_scraped":  {Type: "gauge", Help: "Samples the target exposed in the last scrape."},
}

// storeMetadata records type, help and unit. OpenMetrics counters are
// exposed as <family>_total, so the metadata is also stored under that name,
// which is what queries and the UI look up.
func (m *Manager) storeMetadata(res *ParseResult) {
	names := make([]string, 0, len(res.Families))
	for n := range res.Families {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := res.Families[n]
		md := tsdb.Metadata{Type: f.Type, Help: f.Help, Unit: f.Unit}
		m.db.SetMetadata(n, md)
		if f.Type == "counter" && !strings.HasSuffix(n, "_total") {
			m.db.SetMetadata(n+"_total", md)
		}
	}
}
