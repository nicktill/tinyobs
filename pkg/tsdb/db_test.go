package tsdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
)

func openTest(t *testing.T, opts Options) *DB {
	t.Helper()
	if opts.Dir == "" && !opts.InMemory {
		opts.Dir = t.TempDir()
	}
	db, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustCommit(t *testing.T, app *Appender) CommitResult {
	t.Helper()
	res, err := app.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func eq(name, value string) *labels.Matcher {
	return labels.MustNewMatcher(labels.MatchEqual, name, value)
}

func selectAll(t *testing.T, db *DB, ms ...*labels.Matcher) []Series {
	t.Helper()
	out, err := db.Select(context.Background(), math.MinInt64, math.MaxInt64, 0, ms...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAppendAndSelectAcrossChunks(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	ls := labels.FromStrings("__name__", "up", "job", "api")

	// 500 samples forces several chunk cuts (120 samples per chunk).
	app := db.Appender()
	for i := int64(0); i < 500; i++ {
		app.Append(ls, i*15000, float64(i))
	}
	if res := mustCommit(t, app); res.Appended != 500 {
		t.Fatalf("appended %d, want 500", res.Appended)
	}

	got := selectAll(t, db, eq("__name__", "up"))
	if len(got) != 1 || len(got[0].Samples) != 500 {
		t.Fatalf("got %d series", len(got))
	}
	for i, s := range got[0].Samples {
		if s.T != int64(i)*15000 || s.V != float64(i) {
			t.Fatalf("sample %d = %+v", i, s)
		}
	}

	// Time-bounded query, inclusive on both ends.
	out, err := db.Select(context.Background(), 15000*100, 15000*200, 0, eq("__name__", "up"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(out[0].Samples); n != 101 {
		t.Fatalf("range select returned %d samples, want 101", n)
	}
}

func TestChunkSpanCut(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	ls := labels.FromStrings("__name__", "sparse")
	app := db.Appender()
	// One sample per hour: chunks must be cut by span, not by sample count.
	for i := int64(0); i < 10; i++ {
		app.Append(ls, i*int64(time.Hour/time.Millisecond), 1)
	}
	mustCommit(t, app)
	out, _ := db.Select(context.Background(), 5*3600_000, 7*3600_000, 0, eq("__name__", "sparse"))
	if len(out) != 1 || len(out[0].Samples) != 3 {
		t.Fatalf("got %+v", out)
	}
}

func TestMatchers(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	app := db.Appender()
	for _, code := range []string{"200", "404", "500"} {
		for _, path := range []string{"/a", "/b"} {
			app.Append(labels.FromStrings("__name__", "http_requests_total", "code", code, "path", path), 1000, 1)
		}
	}
	app.Append(labels.FromStrings("__name__", "other", "code", "200"), 1000, 1)
	mustCommit(t, app)

	cases := []struct {
		ms   []*labels.Matcher
		want int
	}{
		{[]*labels.Matcher{eq("__name__", "http_requests_total")}, 6},
		{[]*labels.Matcher{eq("code", "200")}, 3},
		{[]*labels.Matcher{eq("__name__", "http_requests_total"), labels.MustNewMatcher(labels.MatchNotEqual, "code", "200")}, 4},
		{[]*labels.Matcher{eq("__name__", "http_requests_total"), labels.MustNewMatcher(labels.MatchRegexp, "code", "5..|4..")}, 4},
		{[]*labels.Matcher{eq("__name__", "http_requests_total"), labels.MustNewMatcher(labels.MatchNotRegexp, "path", "/a")}, 3},
		{[]*labels.Matcher{labels.MustNewMatcher(labels.MatchRegexp, "__name__", ".+")}, 7},
		// Regex is anchored: "/" alone matches nothing.
		{[]*labels.Matcher{labels.MustNewMatcher(labels.MatchRegexp, "path", "/")}, 0},
		// Absent label matches the empty string.
		{[]*labels.Matcher{eq("__name__", "other"), eq("path", "")}, 1},
	}
	for i, c := range cases {
		if got := len(selectAll(t, db, c.ms...)); got != c.want {
			t.Errorf("case %d %v: got %d series, want %d", i, c.ms, got, c.want)
		}
	}

	if names := db.LabelNames(0, math.MaxInt64); fmt.Sprint(names) != "[__name__ code path]" {
		t.Errorf("label names = %v", names)
	}
	if vals := db.LabelValues("code", 0, math.MaxInt64, eq("__name__", "other")); fmt.Sprint(vals) != "[200]" {
		t.Errorf("label values = %v", vals)
	}
}

func TestOutOfOrderAndDuplicates(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	ls := labels.FromStrings("__name__", "m")
	app := db.Appender()
	app.Append(ls, 2000, 1)
	app.Append(ls, 2000, 1) // exact duplicate: ignored
	app.Append(ls, 2000, 2) // same timestamp, new value: rejected
	app.Append(ls, 1000, 1) // older: rejected
	app.Append(ls, 3000, 3)
	res := mustCommit(t, app)
	if res.Appended != 2 || res.Duplicates != 1 || res.OutOfOrder != 2 {
		t.Fatalf("result = %+v", res)
	}
	if !errors.Is(res.Err(), ErrOutOfOrder) {
		t.Fatalf("err = %v", res.Err())
	}
}

func TestInvalidLabelsAndSeriesLimit(t *testing.T) {
	db := openTest(t, Options{InMemory: true, MaxSeries: 2})
	app := db.Appender()
	app.Append(labels.FromStrings("job", "x"), 1, 1) // no metric name
	app.Append(labels.FromStrings("__name__", "a"), 1, 1)
	app.Append(labels.FromStrings("__name__", "b"), 1, 1)
	app.Append(labels.FromStrings("__name__", "c"), 1, 1) // over the limit
	app.Append(labels.FromStrings("__name__", "a"), 2, 1) // existing series still accepted
	res := mustCommit(t, app)
	if res.Invalid != 1 || res.SeriesCap != 1 || res.Appended != 3 {
		t.Fatalf("result = %+v", res)
	}
}

func TestPersistenceAndResume(t *testing.T) {
	dir := t.TempDir()
	ls := labels.FromStrings("__name__", "requests_total", "job", "api")

	db, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	app := db.Appender()
	for i := int64(0); i < 150; i++ { // one full chunk plus a partial head
		app.Append(ls, i*1000, float64(i))
	}
	mustCommit(t, app)
	if err := db.SetMetadata("requests_total", Metadata{Type: "counter", Help: "Requests."}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Appends continue the reloaded head chunk; stale samples are still rejected.
	app = db.Appender()
	app.Append(ls, 149*1000, 42) // conflicts with stored sample
	for i := int64(150); i < 200; i++ {
		app.Append(ls, i*1000, float64(i))
	}
	res := mustCommit(t, app)
	if res.Appended != 50 || res.OutOfOrder != 1 {
		t.Fatalf("after reopen: %+v", res)
	}

	got := selectAll(t, db, eq("job", "api"))
	if len(got) != 1 || len(got[0].Samples) != 200 {
		t.Fatalf("got %d samples after reopen", len(got[0].Samples))
	}
	for i, s := range got[0].Samples {
		if s.V != float64(i) {
			t.Fatalf("sample %d = %v", i, s.V)
		}
	}
	if md := db.Metadata()["requests_total"]; md.Type != "counter" {
		t.Fatalf("metadata = %+v", md)
	}
	if st := db.Stats(); st.MinTime != 0 || st.MaxTime != 199000 || st.NumSeries != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRetention(t *testing.T) {
	now := time.Unix(100*3600, 0)
	db := openTest(t, Options{InMemory: true, Retention: 10 * time.Hour, Now: func() time.Time { return now }})
	live := labels.FromStrings("__name__", "live")
	dead := labels.FromStrings("__name__", "dead")

	app := db.Appender()
	for ts := now.Add(-30 * time.Hour); ts.Before(now); ts = ts.Add(time.Minute) {
		app.Append(live, ts.UnixMilli(), 1)
		if ts.Before(now.Add(-20 * time.Hour)) {
			app.Append(dead, ts.UnixMilli(), 1)
		}
	}
	mustCommit(t, app)

	if err := db.ApplyRetention(); err != nil {
		t.Fatal(err)
	}
	if n := len(selectAll(t, db, eq("__name__", "dead"))); n != 0 {
		t.Fatalf("expired series still returned")
	}
	if db.Stats().NumSeries != 1 {
		t.Fatalf("expired series still indexed")
	}
	got := selectAll(t, db, eq("__name__", "live"))
	oldest := time.UnixMilli(got[0].Samples[0].T)
	cutoff := now.Add(-10 * time.Hour)
	if oldest.After(cutoff) || oldest.Before(cutoff.Add(-2*time.Hour)) {
		t.Fatalf("oldest sample %v not within one chunk span of cutoff %v", oldest, cutoff)
	}
}

func TestConcurrentAppendAndSelect(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ls := labels.FromStrings("__name__", "m", "worker", fmt.Sprint(w))
			for i := int64(0); i < 300; i++ {
				app := db.Appender()
				app.Append(ls, i, float64(i))
				if _, err := app.Commit(); err != nil {
					t.Error(err)
					return
				}
				if i%50 == 0 {
					if _, err := db.Select(context.Background(), 0, i, 0, eq("__name__", "m")); err != nil {
						t.Error(err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
	got := selectAll(t, db, eq("__name__", "m"))
	if len(got) != 8 {
		t.Fatalf("got %d series", len(got))
	}
	for _, s := range got {
		if len(s.Samples) != 300 {
			t.Fatalf("%s has %d samples", s.Labels, len(s.Samples))
		}
	}
}

func TestSelectSampleLimit(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	app := db.Appender()
	for i := int64(0); i < 100; i++ {
		app.Append(labels.FromStrings("__name__", "m"), i, 1)
	}
	mustCommit(t, app)
	if _, err := db.Select(context.Background(), 0, 1000, 50, eq("__name__", "m")); err == nil {
		t.Fatal("expected sample limit error")
	}
}
