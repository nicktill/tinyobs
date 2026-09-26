package scrape

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

func TestParseTarget(t *testing.T) {
	cases := map[string][2]string{
		"localhost:2112":                   {"localhost:2112", "http://localhost:2112/metrics"},
		"api=localhost:2112":               {"api", "http://localhost:2112/metrics"},
		"api=localhost:2112/custom":        {"api", "http://localhost:2112/custom"},
		"https://example.com:8443/m":       {"example.com:8443", "https://example.com:8443/m"},
		"web=http://10.0.0.1:9100/metrics": {"web", "http://10.0.0.1:9100/metrics"},
	}
	for in, want := range cases {
		tg, err := ParseTarget(in)
		if err != nil || tg.Job != want[0] || tg.URL.String() != want[1] {
			t.Errorf("ParseTarget(%q) = %q %q %v", in, tg.Job, tg.URL, err)
		}
	}
	for _, bad := range []string{"", "ftp://x:1", "=localhost:1", "a b=localhost:1"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q) accepted", bad)
		}
	}
}

func openDB(t *testing.T) *tsdb.DB {
	t.Helper()
	db, err := tsdb.Open(tsdb.Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func latest(t *testing.T, db *tsdb.DB, name string) map[string]float64 {
	t.Helper()
	out, err := db.Select(context.Background(), 0, math.MaxInt64, 0, labels.MustNewMatcher(labels.MatchEqual, "__name__", name))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]float64{}
	for _, s := range out {
		m[s.Labels.String()] = s.Samples[len(s.Samples)-1].V
	}
	return m
}

func TestScrape(t *testing.T) {
	var body atomic.Value
	body.Store("# TYPE requests_total counter\n# HELP requests_total Requests.\nrequests_total{path=\"/a\",job=\"inner\"} 5\nrequests_total{path=\"/b\"} 7\n")
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "boom", 500)
			return
		}
		io.WriteString(w, body.Load().(string))
	}))
	defer srv.Close()

	db := openDB(t)
	tg, _ := ParseTarget("app=" + srv.URL)
	m := NewManager(db, []Target{tg}, 15*time.Second, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Unix(1_000_000, 0)
	inst := tg.Instance()

	m.ScrapeOnce(context.Background(), now)
	got := latest(t, db, "requests_total")
	want := map[string]float64{
		`requests_total{exported_job="inner", instance="` + inst + `", job="app", path="/a"}`: 5,
		`requests_total{instance="` + inst + `", job="app", path="/b"}`:                       7,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("missing %s = %v; got %v", k, v, got)
		}
	}
	if up := latest(t, db, "up"); up[`up{instance="`+inst+`", job="app"}`] != 1 {
		t.Errorf("up = %v", up)
	}
	if md := db.Metadata()["requests_total"]; md.Type != "counter" || md.Help != "Requests." {
		t.Errorf("metadata = %+v", md)
	}
	if st := m.Status()[0]; st.Health != "up" || st.SamplesScraped != 2 {
		t.Errorf("status = %+v", st)
	}

	// A series that disappears gets a staleness marker.
	body.Store("requests_total{path=\"/b\"} 8\n")
	m.ScrapeOnce(context.Background(), now.Add(15*time.Second))
	got = latest(t, db, "requests_total")
	if !tsdb.IsStaleNaN(got[`requests_total{exported_job="inner", instance="`+inst+`", job="app", path="/a"}`]) {
		t.Errorf("vanished series not marked stale: %v", got)
	}

	// A failed scrape marks the target down and all its series stale.
	fail.Store(true)
	m.ScrapeOnce(context.Background(), now.Add(30*time.Second))
	if up := latest(t, db, "up"); up[`up{instance="`+inst+`", job="app"}`] != 0 {
		t.Errorf("up after failure = %v", up)
	}
	if got := latest(t, db, "requests_total"); !tsdb.IsStaleNaN(got[`requests_total{instance="`+inst+`", job="app", path="/b"}`]) {
		t.Errorf("series not stale after failed scrape: %v", got)
	}
	if st := m.Status()[0]; st.Health != "down" || st.LastError == "" {
		t.Errorf("status after failure = %+v", st)
	}
}

func TestScrapeOpenMetricsNegotiation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
		io.WriteString(w, "# TYPE jobs counter\njobs_total 3\njobs_created 1\n# EOF\n")
	}))
	defer srv.Close()
	db := openDB(t)
	tg, _ := ParseTarget(srv.URL)
	m := NewManager(db, []Target{tg}, time.Second, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ScrapeOnce(context.Background(), time.Unix(1000, 0))
	if len(latest(t, db, "jobs_total")) != 1 || len(latest(t, db, "jobs_created")) != 0 {
		t.Fatal("OpenMetrics counter not parsed as expected")
	}
	if db.Metadata()["jobs_total"].Type != "counter" {
		t.Fatal("OpenMetrics counter metadata not stored under the _total name")
	}
}

func TestScrapeLimits(t *testing.T) {
	big := make([]byte, MaxBodyBytes+10)
	for i := range big {
		big[i] = '#'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(big) }))
	defer srv.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()

	db := openDB(t)
	a, _ := ParseTarget("big=" + srv.URL)
	b, _ := ParseTarget("slow=" + slow.URL)
	m := NewManager(db, []Target{a, b}, 100*time.Millisecond, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.ScrapeOnce(context.Background(), time.Unix(1000, 0))
	for _, st := range m.Status() {
		if st.Health != "down" {
			t.Errorf("%s: health %s, want down (%s)", st.Job, st.Health, st.LastError)
		}
	}
}
