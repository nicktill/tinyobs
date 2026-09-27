package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk"
)

func startServer(t *testing.T, dir string) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	srv, err := New(Config{Listen: addr, DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	base := "http://" + addr
	for i := 0; i < 100; i++ {
		if resp, err := http.Get(base + "/-/ready"); err == nil {
			resp.Body.Close()
			return base, cancel, done
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
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

	client, err := sdk.New(sdk.ClientConfig{Service: "e2e", Endpoint: base + "/v1/ingest", FlushEvery: 50 * time.Millisecond})
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

	// The legacy dashboard endpoints are served from the new storage.
	resp, err := http.Get(base + "/v1/query/range?metric=e2e_requests_total")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"route":"/a"`) {
		t.Fatalf("legacy range endpoint: %s", b)
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
	resp, err = http.Get(base + "/api/v1/metadata?metric=e2e_requests_total")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"type":"counter"`) {
		t.Fatalf("metadata after restart: %s", b)
	}
}

func TestSelfMetrics(t *testing.T) {
	base, cancel, done := startServer(t, t.TempDir())
	defer func() { cancel(); <-done }()
	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"tinyobs_series 0", `tinyobs_samples_rejected_total{reason="series_limit"} 0`, "tinyobs_series_per_metric_limit 10000"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}
