package scrape

import (
	"math"
	"strings"
	"testing"
)

const promText = `# HELP http_requests_total Total requests.\nSecond line.
# TYPE http_requests_total counter
http_requests_total{method="GET",path="/a\"b\\c"} 1027 1395066363000
http_requests_total{method="POST", path=""} 3
# A plain comment
# TYPE latency_seconds histogram
latency_seconds_bucket{le="0.1"} 5
latency_seconds_bucket{le="+Inf"} 7
latency_seconds_sum 1.5
latency_seconds_count 7

# TYPE rpc_duration summary
rpc_duration{quantile="0.99"} NaN
rpc_duration_sum 1e3
rpc_duration_count -Inf
untyped_metric{a="1"} 42
`

func TestParsePrometheusText(t *testing.T) {
	res, err := Parse([]byte(promText), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Samples) != 10 {
		t.Fatalf("got %d samples", len(res.Samples))
	}
	s := res.Samples[0]
	if got := s.Labels.String(); got != `http_requests_total{method="GET", path="/a\"b\\c"}` {
		t.Errorf("labels = %s", got)
	}
	if s.V != 1027 || s.T != 1395066363000 {
		t.Errorf("sample = %+v", s)
	}
	if res.Samples[1].Labels.Has("path") {
		t.Error("empty label value should be dropped")
	}
	if !math.IsNaN(res.Samples[6].V) || !math.IsInf(res.Samples[8].V, -1) {
		t.Errorf("special values: %+v %+v", res.Samples[6], res.Samples[8])
	}
	f := res.Families["http_requests_total"]
	if f.Type != "counter" || f.Help != "Total requests.\nSecond line." {
		t.Errorf("family = %+v", f)
	}
	if res.Families["latency_seconds"].Type != "histogram" || res.Families["rpc_duration"].Type != "summary" {
		t.Error("histogram/summary types not recorded")
	}
}

const omText = `# TYPE requests counter
# HELP requests Requests.
# UNIT requests requests
requests_total{code="200"} 10 1700000000.123 # {trace_id="abc"} 1 1700000000.1
requests_created{code="200"} 1600000000
# TYPE temp gauge
temp 21.5
# EOF
`

func TestParseOpenMetrics(t *testing.T) {
	res, err := Parse([]byte(omText), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Samples) != 2 {
		t.Fatalf("got %d samples: %+v (_created must be skipped)", len(res.Samples), res.Samples)
	}
	if s := res.Samples[0]; s.V != 10 || s.T != 1700000000123 {
		t.Errorf("sample with exemplar = %+v", s)
	}
	if res.Families["requests"].Unit != "requests" {
		t.Error("unit not recorded")
	}
	if _, err := Parse([]byte(strings.TrimSuffix(omText, "# EOF\n")), true); err == nil {
		t.Error("missing # EOF accepted")
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		`1metric 1`,
		`metric{a=1} 1`,
		`metric{a="1" 1`,
		`metric{a="1",a="2"} 1`,
		`metric{__name__="x"} 1`,
		`metric{a="\q"} 1`,
		`metric`,
		`metric one`,
		`metric 1 2 3`,
		`metric 1 notatime`,
		`# TYPE metric bogus`,
	} {
		if _, err := Parse([]byte(in+"\n"), false); err == nil {
			t.Errorf("Parse(%q) succeeded", in)
		}
	}
}
