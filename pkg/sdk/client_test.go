package sdk

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/otlp"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// receiver runs TinyObs's OTLP handler on a test server.
func receiver(t *testing.T) (*httptest.Server, *tsdb.DB) {
	t.Helper()
	db, err := tsdb.Open(tsdb.Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mux := http.NewServeMux()
	mux.Handle("POST /v1/metrics", &otlp.Handler{DB: db})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, db
}

func latest(t *testing.T, db *tsdb.DB, ms ...*labels.Matcher) map[string]float64 {
	t.Helper()
	out, err := db.Select(context.Background(), 0, math.MaxInt64, 0, ms...)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]float64{}
	for _, s := range out {
		m[s.Labels.String()] = s.Samples[len(s.Samples)-1].V
	}
	return m
}

func name(n string) *labels.Matcher { return labels.MustNewMatcher(labels.MatchEqual, "__name__", n) }

func TestExportThroughOTLPReceiver(t *testing.T) {
	srv, db := receiver(t)
	c, err := New(ClientConfig{Service: "shop", Instance: "i-1", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	reqs := c.Counter("http_requests_total")
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				reqs.Inc("path", "/cart", "status", "200")
			}
		}()
	}
	wg.Wait()
	reqs.Add(-5, "path", "/cart", "status", "200") // ignored: counters only increase
	c.Gauge("queue_depth").Set(3)
	lat := c.Histogram("http_request_duration_seconds")
	for _, v := range []float64{0.003, 0.02, 0.02, 0.7, 42} {
		lat.Observe(v, "path", "/cart")
	}

	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := latest(t, db, name("http_requests_total")); got[`http_requests_total{instance="i-1", job="shop", path="/cart", status="200"}`] != 1000 {
		t.Fatalf("counter = %v", got)
	}
	if got := latest(t, db, name("queue_depth")); got[`queue_depth{instance="i-1", job="shop"}`] != 3 {
		t.Fatalf("gauge = %v", got)
	}
	buckets := latest(t, db, name("http_request_duration_seconds_bucket"))
	le := func(b string) float64 {
		return buckets[`http_request_duration_seconds_bucket{instance="i-1", job="shop", le="`+b+`", path="/cart"}`]
	}
	// Cumulative buckets with a real +Inf bucket (V1 labelled 10s as +Inf).
	if le("0.005") != 1 || le("0.025") != 3 || le("1") != 4 || le("10") != 4 || le("+Inf") != 5 {
		t.Fatalf("buckets = %v", buckets)
	}
	if got := latest(t, db, name("http_request_duration_seconds_count")); got[`http_request_duration_seconds_count{instance="i-1", job="shop", path="/cart"}`] != 5 {
		t.Fatalf("count = %v", got)
	}
	if got := latest(t, db, name("go_goroutines")); len(got) != 1 {
		t.Fatalf("runtime gauges missing: %v", got)
	}

	// Values are cumulative across exports, not reset after each one.
	time.Sleep(2 * time.Millisecond)
	reqs.Inc("path", "/cart", "status", "200")
	lat.Observe(0.2, "path", "/cart")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := latest(t, db, name("http_requests_total")); got[`http_requests_total{instance="i-1", job="shop", path="/cart", status="200"}`] != 1001 {
		t.Fatalf("counter after second export = %v", got)
	}
	if got := latest(t, db, name("http_request_duration_seconds_count")); got[`http_request_duration_seconds_count{instance="i-1", job="shop", path="/cart"}`] != 6 {
		t.Fatalf("histogram count after second export = %v", got)
	}
}

// Stop must deliver what was recorded before it, even when it arrives
// before the first periodic export.
func TestStopFlushes(t *testing.T) {
	srv, db := receiver(t)
	c, _ := New(ClientConfig{Service: "svc", Instance: "a", Endpoint: srv.URL, FlushEvery: time.Hour})
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err == nil {
		t.Fatal("second Start should fail")
	}
	c.Counter("jobs_total").Add(7)
	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := latest(t, db, name("jobs_total")); got[`jobs_total{instance="a", job="svc"}`] != 7 {
		t.Fatalf("after Stop = %v", got)
	}
	if err := c.Stop(); err != nil {
		t.Fatal("second Stop should be a no-op")
	}
}

func TestEndpointResolution(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "http://localhost:4318/v1/metrics",
		"http://tinyobs:8080":               "http://tinyobs:8080/v1/metrics",
		"http://tinyobs:8080/":              "http://tinyobs:8080/v1/metrics",
		"http://localhost:8080/v1/ingest":   "http://localhost:8080/v1/metrics",
		"https://collector:4318/v1/metrics": "https://collector:4318/v1/metrics",
	} {
		if got, err := otlpURL(in); err != nil || got != want {
			t.Errorf("otlpURL(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := New(ClientConfig{Service: "x", Endpoint: "localhost:8080"}); err == nil {
		t.Error("endpoint without scheme accepted")
	}
	if _, err := New(ClientConfig{}); err == nil {
		t.Error("missing service accepted")
	}
}

func TestExportErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c, _ := New(ClientConfig{Service: "svc", Endpoint: srv.URL})
	if err := c.Flush(context.Background()); err == nil {
		t.Fatal("expected an error from a failing endpoint")
	}
}
