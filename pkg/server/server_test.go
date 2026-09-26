package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nicktill/tinyobs/pkg/scrape"
	"github.com/nicktill/tinyobs/pkg/sdk"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

func startServer(t *testing.T, dir string, targets ...scrape.Target) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	srv, err := New(Config{
		Listen: "127.0.0.1:0", DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ScrapeTargets: targets, ScrapeInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan Addrs, 1)
	go func() { done <- srv.Run(ctx, ready) }()
	select {
	case a := <-ready:
		return "http://" + a.HTTP.String(), cancel, done
	case err := <-done:
		t.Fatal(err)
	}
	return "", nil, nil
}

func promQuery(t *testing.T, base, q string) []map[string]any {
	t.Helper()
	resp, err := http.Get(base + "/api/v1/query?" + url.Values{"query": {q}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []map[string]any `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		t.Fatalf("query %q: %v %s", q, err, body.Error)
	}
	return body.Data.Result
}

// TestEndToEnd sends metrics with the Go SDK, queries them through the
// Prometheus API, restarts the server and queries again.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	base, stop, done := startServer(t, dir)

	client, err := sdk.New(sdk.ClientConfig{Service: "e2e", Endpoint: base, FlushEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	client.Start(context.Background())
	requests := client.Counter("e2e_requests_total")
	for i := 0; i < 250; i++ {
		requests.Inc("route", "/a")
		if i%5 == 0 {
			requests.Inc("route", "/b")
		}
	}
	client.Gauge("e2e_queue_depth").Set(7)
	client.Stop()

	var res []map[string]any
	for i := 0; i < 50; i++ { // the SDK sends asynchronously
		res = promQuery(t, base, `sum by (route) (e2e_requests_total)`)
		if len(res) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := map[string]string{}
	for _, r := range res {
		got[r["metric"].(map[string]any)["route"].(string)] = r["value"].([]any)[1].(string)
	}
	if got["/a"] != "250" || got["/b"] != "50" {
		t.Fatalf("counters after ingest = %v", got)
	}
	if r := promQuery(t, base, `e2e_queue_depth`); len(r) != 1 || r[0]["value"].([]any)[1] != "7" {
		t.Fatalf("gauge = %v", r)
	}

	stop()
	if err := <-done; err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	base, stop, done = startServer(t, dir)
	defer func() { stop(); <-done }()
	if r := promQuery(t, base, `sum(e2e_requests_total)`); len(r) != 1 || r[0]["value"].([]any)[1] != "300" {
		t.Fatalf("after restart = %v", r)
	}
	resp, err := http.Get(base + "/api/v1/metadata?metric=e2e_requests_total")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"type":"counter"`) {
		t.Fatalf("metadata after restart: %s", b)
	}
}

// TestScrapeEndToEnd scrapes a Prometheus endpoint through the running server.
func TestScrapeEndToEnd(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "# TYPE orders_total counter\norders_total{shop=\"eu\"} 12\n")
	}))
	defer app.Close()
	target, err := scrape.ParseTarget("orders=" + app.URL)
	if err != nil {
		t.Fatal(err)
	}
	base, stop, done := startServer(t, t.TempDir(), target)
	defer func() { stop(); <-done }()

	var res []map[string]any
	for i := 0; i < 50; i++ {
		if res = promQuery(t, base, `orders_total{job="orders"}`); len(res) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(res) != 1 || res[0]["value"].([]any)[1] != "12" {
		t.Fatalf("scraped series = %v", res)
	}
	if r := promQuery(t, base, `up{job="orders"}`); len(r) != 1 || r[0]["value"].([]any)[1] != "1" {
		t.Fatalf("up = %v", r)
	}

	resp, err := http.Get(base + "/api/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			ActiveTargets []struct {
				Health     string            `json:"health"`
				ScrapePool string            `json:"scrapePool"`
				Labels     map[string]string `json:"labels"`
			} `json:"activeTargets"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if ts := body.Data.ActiveTargets; len(ts) != 1 || ts[0].Health != "up" || ts[0].ScrapePool != "orders" || ts[0].Labels["instance"] == "" {
		t.Fatalf("targets = %+v", body.Data.ActiveTargets)
	}
}

// TestProductionFeatures covers auth, snapshots, self-metrics, the OTLP port
// and the embedded UI on one running server.
func TestProductionFeatures(t *testing.T) {
	dir := t.TempDir()
	srv, err := New(Config{
		Listen: "127.0.0.1:0", OTLPListen: "127.0.0.1:0", DataDir: dir,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthToken: "s3cret",
		UI:        fstest.MapFS{"index.html": {Data: []byte("<title>TinyObs</title>")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan Addrs, 1)
	go func() { done <- srv.Run(ctx, ready) }()
	a := <-ready
	defer func() { cancel(); <-done }()
	base := "http://" + a.HTTP.String()

	do := func(method, path, auth string, body io.Reader) (int, string) {
		req, _ := http.NewRequest(method, base+path, body)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Auth: required everywhere except health checks; bearer and Basic both work.
	if code, _ := do("GET", "/-/healthy", "", nil); code != 200 {
		t.Errorf("healthy without token: %d", code)
	}
	for _, path := range []string{"/", "/api/v1/labels", "/metrics"} {
		if code, _ := do("GET", path, "", nil); code != 401 {
			t.Errorf("%s without token: %d", path, code)
		}
	}
	if code, _ := do("GET", "/api/v1/labels", "Bearer wrong", nil); code != 401 {
		t.Errorf("wrong token accepted: %d", code)
	}
	if code, _ := do("GET", "/api/v1/labels", "Bearer s3cret", nil); code != 200 {
		t.Errorf("bearer token rejected: %d", code)
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("anyone:s3cret"))
	if code, body := do("GET", "/", basic, nil); code != 200 || !strings.Contains(body, "TinyObs") {
		t.Errorf("UI with Basic auth: %d %q", code, body)
	}

	// OTLP on its own port, also behind auth.
	otlpURL := "http://" + a.OTLP.String() + "/v1/metrics"
	payload := `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"svc"}}]},"scopeMetrics":[{"metrics":[{"name":"jobs","gauge":{"dataPoints":[{"asDouble":3}]}}]}]}]}`
	req, _ := http.NewRequest("POST", otlpURL, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Fatalf("OTLP port without token: %v %v", err, resp)
	}
	req, _ = http.NewRequest("POST", otlpURL, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer s3cret")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("OTLP port with token: %v %v", err, resp)
	}

	// Self-metrics: exposed at /metrics and recorded as job="tinyobs".
	if code, body := do("GET", "/metrics", "Bearer s3cret", nil); code != 200 || !strings.Contains(body, "tinyobs_series ") || !strings.Contains(body, `tinyobs_http_requests_total{code="401",handler="other"}`) {
		t.Errorf("/metrics: %d\n%s", code, body)
	}
	if err := srv.self.record(time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, body := do("GET", "/api/v1/query?query="+url.QueryEscape(`tinyobs_series{job="tinyobs"}`), "Bearer s3cret", nil); code != 200 || !strings.Contains(body, `"tinyobs_series"`) {
		t.Errorf("self-metrics not queryable: %d %s", code, body)
	}

	// Snapshots are written under the data directory and can be restored.
	code, body := do("POST", "/api/v1/admin/tsdb/snapshot", "Bearer s3cret", nil)
	if code != 200 {
		t.Fatalf("snapshot: %d %s", code, body)
	}
	var snap struct {
		Data struct{ Path string } `json:"data"`
	}
	json.Unmarshal([]byte(body), &snap)
	f, err := os.Open(snap.Data.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	restored := t.TempDir()
	if err := tsdb.Restore(restored, f); err != nil {
		t.Fatalf("restoring snapshot: %v", err)
	}
}
