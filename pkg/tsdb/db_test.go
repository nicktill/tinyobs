package tsdb

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
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

func commit(t *testing.T, app *Appender) CommitResult {
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

func TestAppendAndSelect(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	ls := labels.FromStrings("__name__", "up", "job", "api")
	app := db.Appender()
	for i := int64(0); i < 500; i++ {
		app.Append(ls, i*15000, float64(i))
	}
	if res := commit(t, app); res.Appended != 500 {
		t.Fatalf("appended %d", res.Appended)
	}

	got := selectAll(t, db, eq("__name__", "up"))
	if len(got) != 1 || len(got[0].Samples) != 500 {
		t.Fatalf("got %+v", got)
	}
	for i, s := range got[0].Samples {
		if s.T != int64(i)*15000 || s.V != float64(i) {
			t.Fatalf("sample %d = %+v", i, s)
		}
	}
	// Bounds are inclusive on both ends.
	out, _ := db.Select(context.Background(), 15000*100, 15000*200, 0, eq("__name__", "up"))
	if n := len(out[0].Samples); n != 101 {
		t.Fatalf("range select returned %d samples, want 101", n)
	}
	// A range with no samples omits the series.
	out, _ = db.Select(context.Background(), 1, 14999, 0, eq("__name__", "up"))
	if len(out) != 0 {
		t.Fatalf("expected no series, got %d", len(out))
	}
}

func TestNegativeTimestampsSortCorrectly(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	ls := labels.FromStrings("__name__", "m")
	app := db.Appender()
	for _, ts := range []int64{-5000, -1, 0, 1, 7000} {
		app.Append(ls, ts, float64(ts))
	}
	commit(t, app)
	got := selectAll(t, db, eq("__name__", "m"))[0].Samples
	if len(got) != 5 || got[0].T != -5000 || got[4].T != 7000 {
		t.Fatalf("got %+v", got)
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
	commit(t, app)

	re := func(t labels.MatchType, n, v string) *labels.Matcher { return labels.MustNewMatcher(t, n, v) }
	cases := []struct {
		ms   []*labels.Matcher
		want int
	}{
		{[]*labels.Matcher{eq("__name__", "http_requests_total")}, 6},
		{[]*labels.Matcher{eq("code", "200")}, 3},
		{[]*labels.Matcher{eq("__name__", "http_requests_total"), re(labels.MatchNotEqual, "code", "200")}, 4},
		{[]*labels.Matcher{eq("__name__", "http_requests_total"), re(labels.MatchRegexp, "code", "5..|4..")}, 4},
		{[]*labels.Matcher{eq("__name__", "http_requests_total"), re(labels.MatchNotRegexp, "path", "/a")}, 3},
		{[]*labels.Matcher{re(labels.MatchRegexp, "__name__", ".+")}, 7},
		{[]*labels.Matcher{re(labels.MatchRegexp, "path", "/")}, 0},     // anchored
		{[]*labels.Matcher{eq("__name__", "other"), eq("path", "")}, 1}, // absent label matches ""
		{[]*labels.Matcher{eq("__name__", "missing")}, 0},
	}
	for i, c := range cases {
		if got := len(selectAll(t, db, c.ms...)); got != c.want {
			t.Errorf("case %d %v: got %d series, want %d", i, c.ms, got, c.want)
		}
	}
	if got := fmt.Sprint(db.LabelNames(0, math.MaxInt64)); got != "[__name__ code path]" {
		t.Errorf("label names = %s", got)
	}
	if got := fmt.Sprint(db.LabelValues("code", 0, math.MaxInt64, eq("__name__", "other"))); got != "[200]" {
		t.Errorf("label values = %s", got)
	}
	if got := fmt.Sprint(db.LabelValues("code", 5000, 6000)); got != "[]" {
		t.Errorf("label values outside time range = %s", got)
	}
}

func TestOrderingRules(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	ls := labels.FromStrings("__name__", "m")
	app := db.Appender()
	app.Append(ls, 2000, 1)
	app.Append(ls, 2000, 1) // identical: ignored
	app.Append(ls, 2000, 2) // conflicting value: rejected
	app.Append(ls, 1000, 1) // older: rejected
	app.Append(ls, 3000, math.NaN())
	app.Append(ls, 3000, math.NaN()) // identical NaN bits: ignored
	app.Append(ls, 4000, StaleNaN)
	res := commit(t, app)
	if res.Appended != 3 || res.Duplicates != 2 || res.Rejected[ReasonOutOfOrder] != 2 || res.FirstError == nil {
		t.Fatalf("result = %+v", res)
	}
	got := selectAll(t, db)[0].Samples
	if len(got) != 3 || got[0].V != 1 || !math.IsNaN(got[1].V) || !IsStaleNaN(got[2].V) {
		t.Fatalf("stored samples = %+v", got)
	}
}

func TestSelectWithoutMatchersMatchesAll(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	app := db.Appender()
	app.Append(labels.FromStrings("__name__", "a"), 1, 1)
	app.Append(labels.FromStrings("__name__", "b"), 1, 1)
	commit(t, app)
	if n := len(selectAll(t, db)); n != 2 {
		t.Fatalf("got %d", n)
	}
}

func TestInvalidLabelsAndSeriesLimit(t *testing.T) {
	db := openTest(t, Options{InMemory: true, MaxSeries: 2})
	app := db.Appender()
	app.Append(labels.FromStrings("job", "x"), 1, 1)                    // no metric name
	app.Append(labels.FromStrings("__name__", "m", "__x__", "1"), 1, 1) // reserved label
	app.Append(labels.FromStrings("__name__", "a"), 1, 1)
	app.Append(labels.FromStrings("__name__", "b"), 1, 1)
	app.Append(labels.FromStrings("__name__", "c"), 1, 1) // over the cap
	app.Append(labels.FromStrings("__name__", "a"), 2, 1) // existing series still accepted
	res := commit(t, app)
	if res.Rejected[ReasonInvalid] != 2 || res.Rejected[ReasonSeriesCap] != 1 || res.Appended != 3 {
		t.Fatalf("result = %+v", res)
	}
	if !errors.Is(res.FirstError, labels.ErrInvalid) {
		t.Fatalf("first error = %v", res.FirstError)
	}
	st := db.Stats()
	if st.SamplesRejected[ReasonInvalid] != 2 || st.SamplesAppended != 3 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	ls := labels.FromStrings("__name__", "requests_total", "job", "api")

	db, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	app := db.Appender()
	for i := int64(0); i < 150; i++ {
		app.Append(ls, i*1000, float64(i))
	}
	commit(t, app)
	if err := db.SetMetadata("requests_total", Metadata{Type: "counter", Help: "Requests."}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The newest stored sample is recovered, so ordering rules still hold.
	app = db.Appender()
	app.Append(ls, 149*1000, 42) // conflicts with stored value 149
	for i := int64(150); i < 200; i++ {
		app.Append(ls, i*1000, float64(i))
	}
	res := commit(t, app)
	if res.Appended != 50 || res.Rejected[ReasonOutOfOrder] != 1 {
		t.Fatalf("after reopen: %+v", res)
	}
	got := selectAll(t, db, eq("job", "api"))
	if len(got) != 1 || len(got[0].Samples) != 200 {
		t.Fatalf("got %d series", len(got))
	}
	for i, s := range got[0].Samples {
		if s.V != float64(i) {
			t.Fatalf("sample %d = %v", i, s.V)
		}
	}
	if md := db.Metadata()["requests_total"]; md.Type != "counter" || md.Help != "Requests." {
		t.Fatalf("metadata = %+v", md)
	}
	if st := db.Stats(); st.MinTime != 0 || st.MaxTime != 199000 || st.NumSeries != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRetention(t *testing.T) {
	now := time.Unix(1_000_000, 0)
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
	commit(t, app)
	if err := db.ApplyRetention(); err != nil {
		t.Fatal(err)
	}

	if n := len(selectAll(t, db, eq("__name__", "dead"))); n != 0 {
		t.Fatal("expired series still returned")
	}
	if db.Stats().NumSeries != 1 || len(db.LabelValues("__name__", math.MinInt64, math.MaxInt64)) != 1 {
		t.Fatal("expired series still indexed")
	}
	got := selectAll(t, db, eq("__name__", "live"))[0].Samples
	cutoff := now.Add(-10 * time.Hour).UnixMilli()
	if got[0].T < cutoff || got[0].T >= cutoff+60_000 {
		t.Fatalf("oldest sample %d, cutoff %d", got[0].T, cutoff)
	}
	if st := db.Stats(); st.MinTime != got[0].T {
		t.Fatalf("min time not updated: %d", st.MinTime)
	}

	// An expired series can be recreated.
	app = db.Appender()
	app.Append(dead, now.UnixMilli(), 1)
	if res := commit(t, app); res.Appended != 1 {
		t.Fatalf("recreate: %+v", res)
	}
}

func TestConcurrentAppendAndSelect(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ls := labels.FromStrings("__name__", "m", "worker", strconv.Itoa(w))
			for i := int64(0); i < 200; i++ {
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
					db.Stats()
					db.Cardinality(10)
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
		if len(s.Samples) != 200 {
			t.Fatalf("%s has %d samples", s.Labels, len(s.Samples))
		}
	}
}

func TestSampleLimit(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	app := db.Appender()
	for i := int64(0); i < 100; i++ {
		app.Append(labels.FromStrings("__name__", "m"), i, 1)
	}
	commit(t, app)
	if _, err := db.Select(context.Background(), 0, 1000, 50, eq("__name__", "m")); !errors.Is(err, ErrSampleLimit) {
		t.Fatalf("err = %v", err)
	}
}

func TestCardinality(t *testing.T) {
	db := openTest(t, Options{InMemory: true})
	app := db.Appender()
	for i := 0; i < 5; i++ {
		app.Append(labels.FromStrings("__name__", "big", "id", strconv.Itoa(i)), 1, 1)
	}
	app.Append(labels.FromStrings("__name__", "small", "id", "0"), 1, 1)
	commit(t, app)
	c := db.Cardinality(10)
	if c.SeriesCountByMetricName[0] != (Count{"big", 5}) || c.LabelValueCountByLabelName[0] != (Count{"id", 5}) {
		t.Fatalf("cardinality = %+v", c)
	}
}

func TestLabelEncodingRejectsCorruption(t *testing.T) {
	ls := labels.FromStrings("__name__", "m", "job", "api")
	b := encodeLabels(ls)
	if got, err := decodeLabels(b); err != nil || !labels.Equal(got, ls) {
		t.Fatalf("round trip: %v %v", got, err)
	}
	for i := 0; i < len(b); i++ {
		if _, err := decodeLabels(b[:i]); err == nil {
			t.Fatalf("truncated at %d decoded without error", i)
		}
	}
	if _, err := decodeLabels(append(b, 0)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
}

// TestCrashRecovery kills a writer process with SIGKILL mid-ingest and checks
// that every sample it acknowledged is present after reopening.
func TestCrashRecovery(t *testing.T) {
	if os.Getenv("TINYOBS_CRASH_WRITER") != "" {
		crashWriter(os.Getenv("TINYOBS_CRASH_WRITER"))
		return
	}
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecovery$")
	cmd.Env = append(os.Environ(), "TINYOBS_CRASH_WRITER="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	acked := 0
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if n, err := strconv.Atoi(sc.Text()); err == nil {
			acked = n
			if acked >= 50 {
				break
			}
		}
	}
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	if acked < 50 {
		t.Fatalf("writer acknowledged only %d batches", acked)
	}

	db, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer db.Close()
	got := selectAll(t, db, eq("__name__", "crash_test"))
	if len(got) != 10 {
		t.Fatalf("got %d series after crash, want 10", len(got))
	}
	for _, s := range got {
		if len(s.Samples) < acked {
			t.Fatalf("%s: %d samples on disk, %d acknowledged", s.Labels, len(s.Samples), acked)
		}
		for i, smp := range s.Samples {
			if smp.T != int64(i) || smp.V != float64(i) {
				t.Fatalf("%s: sample %d = %+v", s.Labels, i, smp)
			}
		}
	}
}

// crashWriter commits batches forever, printing the number of acknowledged
// batches after each commit, until it is killed.
func crashWriter(dir string) {
	db, err := Open(Options{Dir: dir})
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	for i := int64(0); ; i++ {
		app := db.Appender()
		for s := 0; s < 10; s++ {
			app.Append(labels.FromStrings("__name__", "crash_test", "s", strconv.Itoa(s)), i, float64(i))
		}
		if _, err := app.Commit(); err != nil {
			fmt.Println("commit:", err)
			os.Exit(1)
		}
		fmt.Println(i + 1)
	}
}

func BenchmarkIngest(b *testing.B) {
	db, err := Open(Options{Dir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	sets := make([]labels.Labels, 1000)
	for i := range sets {
		sets[i] = labels.FromStrings("__name__", "http_requests_total", "instance", fmt.Sprint(i%20), "path", fmt.Sprint(i/20))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app := db.Appender()
		for _, ls := range sets {
			app.Append(ls, int64(i)*15000, float64(i))
		}
		if _, err := app.Commit(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N*len(sets))/b.Elapsed().Seconds(), "samples/s")
}

func TestSnapshotRestore(t *testing.T) {
	db := openTest(t, Options{})
	app := db.Appender()
	for i := int64(0); i < 100; i++ {
		app.Append(labels.FromStrings("__name__", "m", "i", strconv.Itoa(int(i%3))), i*1000, float64(i))
	}
	commit(t, app)
	db.SetMetadata("m", Metadata{Type: "gauge"})

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Restore(dir, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := Restore(dir, bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("restore into a non-empty directory must fail")
	}
	restored := openTest(t, Options{Dir: dir})
	got := selectAll(t, restored, eq("__name__", "m"))
	n := 0
	for _, s := range got {
		n += len(s.Samples)
	}
	if len(got) != 3 || n != 100 || restored.Metadata()["m"].Type != "gauge" {
		t.Fatalf("restored %d series, %d samples, metadata %+v", len(got), n, restored.Metadata())
	}
}
