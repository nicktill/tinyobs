package promql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

func testDB(t *testing.T) *tsdb.DB {
	t.Helper()
	db, err := tsdb.Open(tsdb.Options{InMemory: true, Retention: 1000 * time.Hour, Now: func() time.Time { return time.Unix(0, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func add(t *testing.T, db *tsdb.DB, lset labels.Labels, samples ...[2]float64) {
	t.Helper()
	app := db.Appender()
	for _, s := range samples {
		app.Append(lset, int64(s[0]*1000), s[1])
	}
	res, err := app.Commit()
	if err != nil || res.NumRejected() > 0 {
		t.Fatalf("append: %v %v", err, res.FirstError)
	}
}

func instant(t *testing.T, db *tsdb.DB, q string, at float64) Vector {
	t.Helper()
	v, err := NewEngine(db, EngineOptions{}).Instant(context.Background(), q, time.UnixMilli(int64(at*1000)))
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v.(Vector)
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		`rate(x[5m]`:            `expected "," or ")"`,
		`sum(`:                  "unexpected end of input",
		`x{a="b"`:               `unexpected end of input`,
		`x{a=b}`:                "expected string",
		`{a!="b"}`:              "at least one non-empty matcher",
		`foo{__name__="bar"}`:   "metric name must not be set twice",
		`rate(x)`:               "expected type range vector",
		`sum(x[5m])`:            "expected type instant vector",
		`1 > 2`:                 "comparisons between scalars must use BOOL modifier",
		`x and 1`:               "set operator",
		`nosuchfunc(x)`:         `unknown function with name "nosuchfunc"`,
		`x[5m] + 1`:             "binary expression must contain only scalar and instant vector types",
		`x offset 5m offset 1m`: "offset may not be set multiple times",
		`x{a=~"("}`:             "invalid regular expression",
		`topk(x)`:               "wrong number of arguments",
		`"unterminated`:         "unterminated",
		`x $ y`:                 "unexpected character",
		`sum by (a) by (b) (x)`: `expected "("`,
	}
	for q, want := range cases {
		_, err := Parse(q)
		var pe *ParseError
		if err == nil || !errors.As(err, &pe) || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want ParseError containing %q", q, err, want)
		}
		if errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q): malformed input reported as unsupported", q)
		}
	}
}

func TestUnsupportedIsExplicit(t *testing.T) {
	for _, q := range []string{
		`x @ 100`,
		`count_values("v", x)`,
		`holt_winters(x[5m], 0.5, 0.5)`,
		`x[5m+1m]`,
		`x offset step()`,
	} {
		if _, err := Parse(q); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) = %v, want ErrUnsupported", q, err)
		}
	}
}

// Regression tests for the V1 query bugs recorded in docs/design/v2.md.
func TestV1Regressions(t *testing.T) {
	db := testDB(t)
	// Two series scraped at different offsets, as real targets are.
	add(t, db, labels.FromStrings("__name__", "req_total", "code", "200"), [2]float64{0, 10}, [2]float64{15, 20}, [2]float64{30, 30})
	add(t, db, labels.FromStrings("__name__", "req_total", "code", "500"), [2]float64{7, 1}, [2]float64{22, 2}, [2]float64{37, 3})

	// P0-1: aggregation across series whose timestamps never coincide.
	if v := instant(t, db, `sum(req_total)`, 40); len(v) != 1 || v[0].F != 33 {
		t.Errorf("sum across unaligned series = %v, want 33", v)
	}
	// P0-1: arithmetic with a literal.
	if v := instant(t, db, `req_total{code="200"} * 2`, 40); len(v) != 1 || v[0].F != 60 {
		t.Errorf("x * 2 = %v, want 60", v)
	}
	// P0-1: division of two unaligned vectors.
	if v := instant(t, db, `sum(req_total{code="500"}) / sum(req_total)`, 40); len(v) != 1 || math.Abs(v[0].F-3.0/33) > 1e-12 {
		t.Errorf("ratio = %v, want %v", v, 3.0/33)
	}
	// P0-2: negative matchers are honoured.
	if v := instant(t, db, `req_total{code!="200"}`, 40); len(v) != 1 || v[0].Metric.Get("code") != "500" {
		t.Errorf("!= matcher = %v", v)
	}
	if v := instant(t, db, `req_total{code=~"5.."}`, 40); len(v) != 1 {
		t.Errorf("=~ matcher = %v", v)
	}
	// P0-3: malformed queries error instead of evaluating to 0.
	if _, err := NewEngine(db, EngineOptions{}).Instant(context.Background(), `rate(req_total[5m]`, time.Unix(40, 0)); err == nil {
		t.Error("malformed query evaluated without error")
	}
	// P1-15: an instant query is evaluated at the requested time, with lookback.
	v := instant(t, db, `req_total{code="200"}`, 100)
	if len(v) != 1 || v[0].T != 100_000 || v[0].F != 30 {
		t.Errorf("instant query = %+v", v)
	}
	if v := instant(t, db, `req_total`, 1000); len(v) != 0 {
		t.Errorf("sample older than the 5m lookback returned: %v", v)
	}
}

func TestRateOfResettingCounter(t *testing.T) {
	db := testDB(t)
	// 1/s, resetting at t=60.
	var s [][2]float64
	for i := 0; i <= 120; i += 15 {
		v := float64(i)
		if i >= 60 {
			v = float64(i - 60)
		}
		s = append(s, [2]float64{float64(i), v})
	}
	add(t, db, labels.FromStrings("__name__", "c_total"), s...)
	// The window (0s, 120s] holds 15,30,45 | 0,15,30,45,60: an increase of
	// 30 + 60 = 90 over 105s of samples. The 15 counted between the scrape at
	// 45s and the reset at 60s is invisible, as in Prometheus. Extrapolating
	// to the window start gives 90 * 120/105 over 120s.
	v := instant(t, db, `rate(c_total[2m])`, 120)
	if want := 90.0 * 120 / 105 / 120; len(v) != 1 || math.Abs(v[0].F-want) > 1e-12 {
		t.Fatalf("rate across reset = %v, want %v", v, want)
	}
	if v[0].Metric.Has(labels.MetricName) {
		t.Fatal("rate() kept the metric name")
	}
}

func TestHistogramQuantile(t *testing.T) {
	db := testDB(t)
	for le, v := range map[string]float64{"0.1": 50, "0.5": 90, "1": 99, "+Inf": 100} {
		add(t, db, labels.FromStrings("__name__", "lat_bucket", "le", le), [2]float64{0, v})
	}
	v := instant(t, db, `histogram_quantile(0.5, lat_bucket)`, 10)
	if len(v) != 1 || math.Abs(v[0].F-0.1) > 1e-9 {
		t.Fatalf("p50 = %v, want 0.1", v)
	}
	v = instant(t, db, `histogram_quantile(0.95, lat_bucket)`, 10)
	want := 0.5 + (1-0.5)*((95.0-90)/(99-90))
	if len(v) != 1 || math.Abs(v[0].F-want) > 1e-9 {
		t.Fatalf("p95 = %v, want %v", v, want)
	}
}

func TestRangeQuery(t *testing.T) {
	db := testDB(t)
	var s [][2]float64
	for i := 0; i <= 600; i += 10 {
		s = append(s, [2]float64{float64(i), float64(i)})
	}
	add(t, db, labels.FromStrings("__name__", "c_total", "job", "api"), s...)
	m, err := NewEngine(db, EngineOptions{}).Range(context.Background(), `rate(c_total[1m])`, time.Unix(60, 0), time.Unix(600, 0), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || len(m[0].Points) != 10 {
		t.Fatalf("range result = %+v", m)
	}
	for _, p := range m[0].Points {
		if math.Abs(p.F-1) > 1e-9 {
			t.Fatalf("rate point %+v, want 1", p)
		}
	}
	if _, err := NewEngine(db, EngineOptions{}).Range(context.Background(), `c_total[1m]`, time.Unix(0, 0), time.Unix(60, 0), time.Second); err == nil {
		t.Fatal("range query of a range vector should fail")
	}
	if _, err := NewEngine(db, EngineOptions{MaxPoints: 10}).Range(context.Background(), `c_total`, time.Unix(0, 0), time.Unix(600, 0), time.Second); err == nil {
		t.Fatal("expected max points error")
	}
}

func TestLimits(t *testing.T) {
	db := testDB(t)
	var s [][2]float64
	for i := 0; i < 1000; i++ {
		s = append(s, [2]float64{float64(i), 1})
	}
	add(t, db, labels.FromStrings("__name__", "m"), s...)
	eng := NewEngine(db, EngineOptions{MaxSamples: 100})
	if _, err := eng.Instant(context.Background(), `sum_over_time(m[1h])`, time.Unix(1000, 0)); err == nil || !strings.Contains(err.Error(), "too many samples") {
		t.Fatalf("sample limit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewEngine(db, EngineOptions{}).Instant(ctx, `m`, time.Unix(10, 0)); err == nil {
		t.Fatal("cancelled context not honoured")
	}
}

func TestStalenessEndsSeries(t *testing.T) {
	db := testDB(t)
	add(t, db, labels.FromStrings("__name__", "up", "instance", "a"), [2]float64{0, 1}, [2]float64{15, 1}, [2]float64{30, tsdb.StaleNaN})
	if v := instant(t, db, `up`, 20); len(v) != 1 {
		t.Fatalf("before stale marker: %v", v)
	}
	if v := instant(t, db, `up`, 31); len(v) != 0 {
		t.Fatalf("after stale marker the series should be gone within the lookback, got %v", v)
	}
	if v := instant(t, db, `count_over_time(up[1m])`, 31); len(v) != 1 || v[0].F != 2 {
		t.Fatalf("stale markers must not count as samples: %v", v)
	}
}

func TestConcurrentQueries(t *testing.T) {
	db := testDB(t)
	for i := 0; i < 20; i++ {
		var s [][2]float64
		for ts := 0; ts <= 600; ts += 15 {
			s = append(s, [2]float64{float64(ts), float64(ts * i)})
		}
		add(t, db, labels.FromStrings("__name__", "m_total", "i", fmt.Sprint(i)), s...)
	}
	eng := NewEngine(db, EngineOptions{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := eng.Range(context.Background(), `sum(rate(m_total[1m])) / count(m_total)`, time.Unix(60, 0), time.Unix(600, 0), 15*time.Second); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkRangeQuery(b *testing.B) {
	db, _ := tsdb.Open(tsdb.Options{InMemory: true, Retention: 1000 * time.Hour})
	defer db.Close()
	now := time.Now()
	start := now.Add(-time.Hour)
	for i := 0; i < 1000; i++ {
		app := db.Appender()
		ls := labels.FromStrings("__name__", "http_requests_total", "instance", fmt.Sprint(i%10), "route", fmt.Sprint(i/10))
		for ts := start; ts.Before(now); ts = ts.Add(15 * time.Second) {
			app.Append(ls, ts.UnixMilli(), float64(ts.Unix()-start.Unix()))
		}
		app.Commit()
	}
	eng := NewEngine(db, EngineOptions{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := eng.Range(context.Background(), `sum by (instance) (rate(http_requests_total[5m]))`, start, now, 15*time.Second); err != nil {
			b.Fatal(err)
		}
	}
}
