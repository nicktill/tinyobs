package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/promql"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

func setup(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := tsdb.Open(tsdb.Options{InMemory: true, Retention: 1000 * time.Hour, Now: func() time.Time { return time.Unix(1000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	app := db.Appender()
	for ts := int64(0); ts <= 600; ts += 15 {
		app.Append(labels.FromStrings("__name__", "http_requests_total", "job", "api", "code", "200"), ts*1000, float64(ts))
		app.Append(labels.FromStrings("__name__", "http_requests_total", "job", "api", "code", "500"), ts*1000, float64(ts)/10)
		app.Append(labels.FromStrings("__name__", "up", "job", "api"), ts*1000, 1)
	}
	if _, err := app.Commit(); err != nil {
		t.Fatal(err)
	}
	db.SetMetadata("http_requests_total", tsdb.Metadata{Type: "counter", Help: "Requests served."})

	mux := http.NewServeMux()
	(&API{
		DB:        db,
		Engine:    promql.NewEngine(db, promql.EngineOptions{}),
		BuildInfo: BuildInfo{Version: "2.0.0"},
		Now:       func() time.Time { return time.Unix(600, 0) },
	}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, params url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(srv.URL + path + "?" + params.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%s: invalid JSON %q", path, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: content type %q", path, ct)
	}
	return resp.StatusCode, out
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestInstantQueryShape(t *testing.T) {
	srv := setup(t)
	code, body := get(t, srv, "/api/v1/query", url.Values{"query": {`sum by (code) (http_requests_total)`}, "time": {"600"}})
	if code != 200 || body["status"] != "success" {
		t.Fatalf("%d %v", code, body)
	}
	want := `{"result":[{"metric":{"code":"200"},"value":[600,"600"]},{"metric":{"code":"500"},"value":[600,"60"]}],"resultType":"vector"}`
	if got := asJSON(body["data"]); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	// Scalars and default time.
	_, body = get(t, srv, "/api/v1/query", url.Values{"query": {`1 + 1`}})
	if got := asJSON(body["data"]); got != `{"result":[600,"2"],"resultType":"scalar"}` {
		t.Fatalf("scalar: %s", got)
	}
	// Special float values are strings.
	_, body = get(t, srv, "/api/v1/query", url.Values{"query": {`vector(1/0)`}, "time": {"600"}})
	if got := asJSON(body["data"]); !strings.Contains(got, `"+Inf"`) {
		t.Fatalf("inf: %s", got)
	}
}

func TestRangeQueryShape(t *testing.T) {
	srv := setup(t)
	_, body := get(t, srv, "/api/v1/query_range", url.Values{
		"query": {`up`}, "start": {"2023-01-01T00:00:00Z"}, "end": {"2023-01-01T00:01:00Z"}, "step": {"30s"},
	})
	if got := asJSON(body["data"]); got != `{"result":[],"resultType":"matrix"}` {
		t.Fatalf("empty range: %s", got)
	}
	_, body = get(t, srv, "/api/v1/query_range", url.Values{"query": {`up`}, "start": {"540"}, "end": {"600"}, "step": {"30"}})
	want := `{"result":[{"metric":{"__name__":"up","job":"api"},"values":[[540,"1"],[570,"1"],[600,"1"]]}],"resultType":"matrix"}`
	if got := asJSON(body["data"]); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestErrors(t *testing.T) {
	srv := setup(t)
	cases := []struct {
		path   string
		params url.Values
		code   int
		typ    string
	}{
		{"/api/v1/query", url.Values{}, 400, "bad_data"},
		{"/api/v1/query", url.Values{"query": {`rate(x[5m]`}}, 400, "bad_data"},
		{"/api/v1/query", url.Values{"query": {`up`}, "time": {"yesterday"}}, 400, "bad_data"},
		{"/api/v1/query", url.Values{"query": {`x @ 100`}}, 400, "bad_data"},
		{"/api/v1/query", url.Values{"query": {`x`}, "timeout": {"forever"}}, 400, "bad_data"},
		{"/api/v1/query_range", url.Values{"query": {`up`}, "start": {"600"}, "end": {"0"}, "step": {"15"}}, 400, "bad_data"},
		{"/api/v1/query_range", url.Values{"query": {`up`}, "start": {"0"}, "end": {"600"}, "step": {"0"}}, 400, "bad_data"},
		{"/api/v1/query_range", url.Values{"query": {`up`}, "start": {"0"}, "end": {"600"}}, 400, "bad_data"},
		{"/api/v1/query_range", url.Values{"query": {`up`}, "start": {"0"}, "end": {"1000000"}, "step": {"1"}}, 422, "execution"},
		{"/api/v1/query", url.Values{"query": {`label_replace(up, "a", "", "", "(")`}}, 422, "execution"},
		{"/api/v1/series", url.Values{}, 400, "bad_data"},
		{"/api/v1/series", url.Values{"match[]": {`rate(up[5m])`}}, 400, "bad_data"},
		{"/api/v1/label/bad-name/values", url.Values{}, 400, "bad_data"},
		{"/api/v1/labels", url.Values{"limit": {"-1"}}, 400, "bad_data"},
	}
	for _, c := range cases {
		code, body := get(t, srv, c.path, c.params)
		if code != c.code || body["status"] != "error" || body["errorType"] != c.typ || body["error"] == "" {
			t.Errorf("%s %v: got %d %v, want %d %s", c.path, c.params, code, body, c.code, c.typ)
		}
	}
}

func TestDiscovery(t *testing.T) {
	srv := setup(t)
	_, body := get(t, srv, "/api/v1/labels", nil)
	if got := asJSON(body["data"]); got != `["__name__","code","job"]` {
		t.Errorf("labels: %s", got)
	}
	_, body = get(t, srv, "/api/v1/label/__name__/values", nil)
	if got := asJSON(body["data"]); got != `["http_requests_total","up"]` {
		t.Errorf("metric names: %s", got)
	}
	_, body = get(t, srv, "/api/v1/label/code/values", url.Values{"match[]": {`http_requests_total{code=~"5.."}`}})
	if got := asJSON(body["data"]); got != `["500"]` {
		t.Errorf("label values with match: %s", got)
	}
	_, body = get(t, srv, "/api/v1/series", url.Values{"match[]": {`up`, `http_requests_total{code="200"}`}})
	if got := asJSON(body["data"]); got != `[{"__name__":"http_requests_total","code":"200","job":"api"},{"__name__":"up","job":"api"}]` {
		t.Errorf("series: %s", got)
	}
	_, body = get(t, srv, "/api/v1/series", url.Values{"match[]": {`up`}, "start": {"5000"}})
	if got := asJSON(body["data"]); got != `[]` {
		t.Errorf("series outside range: %s", got)
	}
	_, body = get(t, srv, "/api/v1/metadata", nil)
	if got := asJSON(body["data"]); got != `{"http_requests_total":[{"help":"Requests served.","type":"counter","unit":""}]}` {
		t.Errorf("metadata: %s", got)
	}
	_, body = get(t, srv, "/api/v1/status/buildinfo", nil)
	if bi := body["data"].(map[string]any); bi["version"] != "2.0.0" || bi["goVersion"] == "" {
		t.Errorf("buildinfo: %v", bi)
	}
	_, body = get(t, srv, "/api/v1/status/tsdb", nil)
	d := body["data"].(map[string]any)
	if asJSON(d["seriesCountByMetricName"]) != `[{"name":"http_requests_total","value":2},{"name":"up","value":1}]` || d["headStats"].(map[string]any)["numSeries"] != float64(3) {
		t.Errorf("tsdb status: %s", asJSON(d))
	}
}

func TestPOSTForm(t *testing.T) {
	srv := setup(t)
	resp, err := http.PostForm(srv.URL+"/api/v1/query", url.Values{"query": {`up`}, "time": {"600"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != 200 || !strings.Contains(asJSON(body["data"]), `"up"`) {
		t.Fatalf("POST query: %d %v", resp.StatusCode, body)
	}
}

func TestParseTime(t *testing.T) {
	for in, want := range map[string]int64{
		"1435781451.781":           1435781451781,
		"1435781451":               1435781451000,
		"2015-07-01T20:10:51.781Z": 1435781451781,
	} {
		got, err := parseTime(in)
		if err != nil || got.UnixMilli() != want {
			t.Errorf("parseTime(%q) = %v %v", in, got.UnixMilli(), err)
		}
	}
	for in, want := range map[string]time.Duration{"15": 15 * time.Second, "0.5": 500 * time.Millisecond, "1m30s": 90 * time.Second} {
		if got, err := parseDuration(in); err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v %v", in, got, err)
		}
	}
}
