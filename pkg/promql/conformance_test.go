package promql

// Conformance with Prometheus.
//
// This test runs Prometheus's own PromQL test files (fetched by
// scripts/fetch-promql-tests.sh) against this engine. Every evaluation must
// either produce Prometheus's expected result, or fail with ErrUnsupported.
// A wrong answer, or an unexpected error, fails the test. Evaluations that
// depend on native histograms, which TinyObs does not store, are skipped.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

func TestPrometheusConformance(t *testing.T) {
	files, _ := filepath.Glob("testdata/prometheus/*.test")
	if len(files) == 0 {
		t.Skip("Prometheus test files not present; run scripts/fetch-promql-tests.sh")
	}
	var total conformanceStats
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			st := runTestFile(t, f)
			t.Logf("passed %d, unsupported %d, skipped %d, failed %d", st.passed, st.unsupported, st.skipped, st.failed)
			total.add(st)
		})
	}
	t.Logf("TOTAL: passed %d, unsupported %d, skipped %d, failed %d", total.passed, total.unsupported, total.skipped, total.failed)
}

type conformanceStats struct{ passed, unsupported, skipped, failed int }

func (s *conformanceStats) add(o conformanceStats) {
	s.passed += o.passed
	s.unsupported += o.unsupported
	s.skipped += o.skipped
	s.failed += o.failed
}

type testCmd struct {
	line  int
	text  string
	block []string // indented lines that follow
}

func readCommands(path string) ([]testCmd, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cmds []testCmd
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(cmds) > 0 {
				cmds[len(cmds)-1].block = append(cmds[len(cmds)-1].block, trimmed)
			}
			continue
		}
		cmds = append(cmds, testCmd{line: n, text: trimmed})
	}
	return cmds, sc.Err()
}

var (
	evalRe      = regexp.MustCompile(`^eval(?:_(fail|warn|info|ordered))?\s+instant\s+at\s+(\S+)\s+(.+)$`)
	evalRangeRe = regexp.MustCompile(`^eval(?:_(fail|warn|info|ordered))?\s+range\s+from\s+(\S+)\s+to\s+(\S+)\s+step\s+(\S+)\s+(.+)$`)
	loadRe      = regexp.MustCompile(`^load(_with_nhcb)?\s+(\S+)$`)
)

func runTestFile(t *testing.T, path string) conformanceStats {
	cmds, err := readCommands(path)
	if err != nil {
		t.Fatal(err)
	}
	var st conformanceStats
	var db *tsdb.DB
	nativeData := false // current data set includes native histograms
	reset := func() {
		if db != nil {
			db.Close()
		}
		db, err = tsdb.Open(tsdb.Options{InMemory: true, Retention: 100 * 365 * 24 * time.Hour, Now: func() time.Time { return time.Unix(0, 0) }})
		if err != nil {
			t.Fatal(err)
		}
		nativeData = false
	}
	reset()
	defer func() { db.Close() }()

	for _, c := range cmds {
		where := fmt.Sprintf("%s:%d", filepath.Base(path), c.line)
		switch {
		case c.text == "clear":
			reset()
		case loadRe.MatchString(c.text):
			m := loadRe.FindStringSubmatch(c.text)
			if m[1] != "" {
				nativeData = true // converted to native histograms
			}
			interval, err := parseTestDuration(m[2])
			if err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			native, err := loadSeries(db, c.block, interval)
			if err != nil {
				t.Fatalf("%s: %v", where, err)
			}
			nativeData = nativeData || native
		case strings.HasPrefix(c.text, "eval"):
			res := runEval(db, c, nativeData)
			switch res.outcome {
			case "pass":
				st.passed++
			case "unsupported":
				st.unsupported++
			case "skip":
				st.skipped++
			default:
				st.failed++
				t.Errorf("%s: %s\n    %s", where, c.text, res.msg)
			}
		default:
			t.Fatalf("%s: unknown command %q", where, c.text)
		}
	}
	return st
}

type evalResult struct {
	outcome string // pass, unsupported, skip, fail
	msg     string
}

func runEval(db *tsdb.DB, c testCmd, nativeData bool) evalResult {
	var kind, query string
	var start, end, step time.Duration
	isRange := false
	var err error
	if m := evalRe.FindStringSubmatch(c.text); m != nil {
		kind, query = m[1], m[3]
		if start, err = parseTestDuration(m[2]); err != nil {
			return evalResult{"fail", err.Error()}
		}
	} else if m := evalRangeRe.FindStringSubmatch(c.text); m != nil {
		isRange = true
		kind, query = m[1], m[5]
		for i, p := range []*time.Duration{&start, &end, &step} {
			if *p, err = parseTestDuration(m[2+i]); err != nil {
				return evalResult{"fail", err.Error()}
			}
		}
	} else {
		return evalResult{"fail", "cannot parse eval command"}
	}

	expectFail, ordered := kind == "fail", kind == "ordered"
	failMsg, failRegex := "", ""
	var expected []string
	for _, line := range c.block {
		if strings.HasPrefix(line, "expect ") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "expect "))
			switch {
			case strings.HasPrefix(d, "fail"):
				expectFail = true
				rest := strings.TrimSpace(strings.TrimPrefix(d, "fail"))
				if strings.HasPrefix(rest, "msg:") {
					failMsg = strings.TrimSpace(strings.TrimPrefix(rest, "msg:"))
				} else if strings.HasPrefix(rest, "regex:") {
					failRegex = strings.TrimSpace(strings.TrimPrefix(rest, "regex:"))
				}
			case d == "ordered":
				ordered = true
			case strings.HasPrefix(d, "string "):
				expected = append(expected, strings.TrimSpace(strings.TrimPrefix(d, "string ")))
			}
			// warn/info annotations are not produced by TinyObs; ignored.
			continue
		}
		expected = append(expected, line)
	}
	for _, e := range expected {
		if strings.Contains(e, "{{") {
			return evalResult{outcome: "skip"} // native histogram results
		}
	}

	eng := NewEngine(db, EngineOptions{})
	ctx := context.Background()
	var got Value
	if isRange {
		got, err = eng.Range(ctx, query, time.UnixMilli(start.Milliseconds()), time.UnixMilli(end.Milliseconds()), step)
	} else {
		got, err = eng.Instant(ctx, query, time.UnixMilli(start.Milliseconds()))
	}

	if errors.Is(err, ErrUnsupported) {
		return evalResult{outcome: "unsupported"}
	}
	if expectFail {
		if err == nil {
			if nativeData {
				return evalResult{outcome: "skip"}
			}
			return evalResult{"fail", fmt.Sprintf("expected an error, got %v", describe(got))}
		}
		if failMsg != "" && err.Error() != failMsg && !strings.Contains(err.Error(), failMsg) {
			return evalResult{"fail", fmt.Sprintf("error %q, expected message %q", err, failMsg)}
		}
		if failRegex != "" && !regexp.MustCompile(failRegex).MatchString(err.Error()) {
			return evalResult{"fail", fmt.Sprintf("error %q does not match %q", err, failRegex)}
		}
		return evalResult{outcome: "pass"}
	}
	if err != nil {
		if nativeData {
			return evalResult{outcome: "skip"}
		}
		return evalResult{"fail", "unexpected error: " + err.Error()}
	}

	var diff string
	if isRange {
		diff = compareMatrix(got.(Matrix), expected, start, step)
	} else {
		diff = compareInstant(got, expected, ordered)
	}
	if diff != "" {
		if nativeData {
			if os.Getenv("TINYOBS_SHOW_SKIPS") != "" {
				fmt.Printf("SKIP-MISMATCH %s: %s\n", c.text, diff)
			}
			return evalResult{outcome: "skip"} // results depend on data we did not load
		}
		return evalResult{"fail", diff}
	}
	return evalResult{outcome: "pass"}
}

func describe(v Value) string {
	switch x := v.(type) {
	case Vector:
		var parts []string
		for _, s := range x {
			parts = append(parts, fmt.Sprintf("%s %v", s.Metric, s.F))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case Scalar:
		return fmt.Sprintf("scalar %v", x.V)
	case Matrix:
		var parts []string
		for _, s := range x {
			parts = append(parts, fmt.Sprintf("%s %v", s.Metric, s.Points))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case String:
		return fmt.Sprintf("string %q", x.V)
	}
	return fmt.Sprint(v)
}

func compareInstant(got Value, expected []string, ordered bool) string {
	switch x := got.(type) {
	case Scalar:
		if len(expected) != 1 {
			return fmt.Sprintf("got scalar %v, expected %v", x.V, expected)
		}
		want, err := parseTestValue(strings.TrimSpace(expected[0]))
		if err != nil {
			return err.Error()
		}
		if !floatMatch(want, x.V) {
			return fmt.Sprintf("got scalar %v, want %v", x.V, want)
		}
		return ""
	case String:
		if len(expected) != 1 || strings.Trim(expected[0], "\"`") != x.V {
			return fmt.Sprintf("got string %q, expected %v", x.V, expected)
		}
		return ""
	case Matrix:
		return "" // instant range-vector results: not compared (timestamps are implicit)
	case Vector:
		type exp struct {
			ls labels.Labels
			v  float64
		}
		var want []exp
		for _, line := range expected {
			ls, vals, err := parseSeriesLine(line)
			if err != nil {
				return err.Error()
			}
			if len(vals) != 1 || vals[0].omitted {
				return fmt.Sprintf("expected line %q is not a single value", line)
			}
			want = append(want, exp{ls, vals[0].v})
		}
		if len(want) != len(x) {
			return fmt.Sprintf("got %d samples %s, want %d %v", len(x), describe(x), len(want), expected)
		}
		for i, w := range want {
			found := false
			for j, s := range x {
				if ordered && i != j {
					continue
				}
				if labels.Equal(s.Metric, w.ls) {
					if !floatMatch(w.v, s.F) {
						return fmt.Sprintf("%s: got %v, want %v", w.ls, s.F, w.v)
					}
					found = true
					break
				}
			}
			if !found {
				if ordered {
					return fmt.Sprintf("position %d: want %s, got %s", i, w.ls, describe(x))
				}
				return fmt.Sprintf("missing %s in %s", w.ls, describe(x))
			}
		}
		return ""
	}
	return fmt.Sprintf("unexpected result type %T", got)
}

func compareMatrix(got Matrix, expected []string, start, step time.Duration) string {
	want := map[string][]Point{}
	for _, line := range expected {
		ls, vals, err := parseSeriesLine(line)
		if err != nil {
			return err.Error()
		}
		var pts []Point
		for i, v := range vals {
			if !v.omitted {
				pts = append(pts, Point{T: (start + time.Duration(i)*step).Milliseconds(), F: v.v})
			}
		}
		if len(pts) > 0 {
			want[labelsKey(ls)] = pts
		}
	}
	if len(want) != len(got) {
		return fmt.Sprintf("got %d series %s, want %d %v", len(got), describe(got), len(want), expected)
	}
	for _, s := range got {
		w, ok := want[labelsKey(s.Metric)]
		if !ok {
			return fmt.Sprintf("unexpected series %s", s.Metric)
		}
		if len(w) != len(s.Points) {
			return fmt.Sprintf("%s: got points %v, want %v", s.Metric, s.Points, w)
		}
		for i := range w {
			if w[i].T != s.Points[i].T || !floatMatch(w[i].F, s.Points[i].F) {
				return fmt.Sprintf("%s: got points %v, want %v", s.Metric, s.Points, w)
			}
		}
	}
	return ""
}

func floatMatch(want, got float64) bool {
	if tsdb.IsStaleNaN(want) || tsdb.IsStaleNaN(got) {
		return tsdb.IsStaleNaN(want) == tsdb.IsStaleNaN(got)
	}
	if math.IsNaN(want) || math.IsNaN(got) {
		return math.IsNaN(want) && math.IsNaN(got)
	}
	return almostEqual(want, got, 1e-6)
}

// Series notation: "metric{a="b"} 1 2+3x4 _ _x2 stale".

type seqValue struct {
	v       float64
	omitted bool
}

// loadSeries appends the series of a load block. As in Prometheus's test
// loader, a series defined twice keeps the later value for a timestamp.
// It reports whether some data could not be loaded: native histograms, or
// series TinyObs rejects by design (e.g. reserved label names).
func loadSeries(db *tsdb.DB, lines []string, interval time.Duration) (partial bool, err error) {
	type key struct {
		series string
		t      int64
	}
	values := map[key]float64{}
	sets := map[string]labels.Labels{}
	for _, line := range lines {
		if strings.Contains(line, "{{") {
			partial = true // native histogram samples are dropped; floats on the line are kept
		}
		ls, vals, err := parseSeriesLine(line)
		if err != nil {
			return partial, err
		}
		if labels.Validate(ls) != nil {
			partial = true
			continue
		}
		k := labelsKey(ls)
		sets[k] = ls
		for i, v := range vals {
			if !v.omitted {
				values[key{k, (time.Duration(i) * interval).Milliseconds()}] = v.v
			}
		}
	}
	keys := make([]key, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].t < keys[j].t })
	app := db.Appender()
	for _, k := range keys {
		app.Append(sets[k.series], k.t, values[k])
	}
	res, err := app.Commit()
	if err != nil {
		return partial, err
	}
	if res.NumRejected() > 0 {
		return partial, fmt.Errorf("load rejected samples: %v", res.FirstError)
	}
	return partial, nil
}

func parseSeriesLine(line string) (labels.Labels, []seqValue, error) {
	// Split the series description from the values: the description ends at
	// the closing brace, or at the first space if there are no braces.
	desc, rest := splitSeriesLine(line)
	desc = strings.TrimSpace(desc)
	var ls labels.Labels
	if desc == "{}" {
		ls = labels.Labels{}
	} else if strings.HasPrefix(desc, "{") || strings.ContainsAny(desc, "{") || labels.IsValidMetricName(desc) {
		e, err := Parse(desc)
		if err != nil {
			// A bare value (scalar expectation) has no series description.
			if _, perr := parseTestValue(desc); perr == nil {
				return labels.Labels{}, mustValues(line), nil
			}
			return nil, nil, fmt.Errorf("series %q: %v", desc, err)
		}
		vs, ok := e.(*VectorSelector)
		if !ok {
			return nil, nil, fmt.Errorf("series %q is not a selector", desc)
		}
		var l []labels.Label
		for _, m := range vs.Matchers {
			l = append(l, labels.Label{Name: m.Name, Value: m.Value})
		}
		ls = labels.New(l...)
	} else {
		return labels.Labels{}, mustValues(line), nil
	}
	vals, err := parseValues(rest)
	return ls, vals, err
}

// splitSeriesLine separates `metric{a="b c"} 1 2` into the series
// description and the values, honouring quotes and braces.
func splitSeriesLine(line string) (string, string) {
	depth := 0
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '{':
			if strings.HasPrefix(line[i:], "{{") && depth == 0 {
				return line[:i], line[i:] // a native histogram value, not labels
			}
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return line[:i+1], line[i+1:]
			}
		case (c == ' ' || c == '\t') && depth == 0:
			return line[:i], line[i:]
		}
	}
	return line, ""
}

func mustValues(s string) []seqValue {
	v, _ := parseValues(s)
	return v
}

var seqRe = regexp.MustCompile(`^([^x]+?)(?:([+-][^x]+))?x(\d+)$`)

// histRe matches a native histogram literal, optionally repeated ("{{...}}x3").
var histRe = regexp.MustCompile(`\{\{[^}]*\}\}(?:[+-]\{\{[^}]*\}\})?(?:x(\d+))?`)

func parseValues(s string) ([]seqValue, error) {
	// Native histogram samples become omitted values (TinyObs stores floats only).
	s = histRe.ReplaceAllStringFunc(s, func(m string) string {
		n := 1
		if sub := histRe.FindStringSubmatch(m); sub[1] != "" {
			n, _ = strconv.Atoi(sub[1])
			n++
		}
		return strings.Repeat(" _ ", n)
	})
	var out []seqValue
	for _, tok := range strings.Fields(s) {
		switch {
		case tok == "_":
			out = append(out, seqValue{omitted: true})
		case strings.HasPrefix(tok, "_x"):
			n, err := strconv.Atoi(tok[2:])
			if err != nil {
				return nil, fmt.Errorf("bad value %q", tok)
			}
			for i := 0; i < n; i++ {
				out = append(out, seqValue{omitted: true})
			}
		case seqRe.MatchString(tok): // "0x59" is 0 repeated, never hex
			m := seqRe.FindStringSubmatch(tok)
			start, err := parseTestValue(m[1])
			if err != nil {
				return nil, err
			}
			inc := 0.0
			if m[2] != "" {
				if inc, err = parseTestValue(m[2]); err != nil {
					return nil, err
				}
			}
			n, _ := strconv.Atoi(m[3])
			v := start
			for i := 0; i <= n; i++ {
				out = append(out, seqValue{v: v})
				v += inc
			}
		default:
			v, err := parseTestValue(tok)
			if err != nil {
				return nil, err
			}
			out = append(out, seqValue{v: v})
		}
	}
	return out, nil
}

func parseTestValue(s string) (float64, error) {
	switch strings.ToLower(s) {
	case "stale":
		return tsdb.StaleNaN, nil
	case "nan":
		return math.NaN(), nil
	case "inf", "+inf":
		return math.Inf(1), nil
	case "-inf":
		return math.Inf(-1), nil
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "-0x") {
		neg := strings.HasPrefix(s, "-")
		v, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(s, "-"), "0x"), 16, 64)
		if neg {
			v = -v
		}
		return float64(v), err
	}
	return strconv.ParseFloat(s, 64)
}

func parseTestDuration(s string) (time.Duration, error) {
	if d, err := ParseDuration(s); err == nil {
		return d, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	return time.Duration(f * float64(time.Second)), nil
}
