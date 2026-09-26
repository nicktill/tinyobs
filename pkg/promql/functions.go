package promql

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nicktill/tinyobs/pkg/labels"
)

// function describes a PromQL function: its signature and implementation.
type function struct {
	Name       string
	ArgTypes   []ValueType
	Optional   int  // trailing arguments that may be omitted
	Variadic   bool // the last argument type repeats
	ReturnType ValueType
	// KeepName keeps the metric name on results; most functions drop it.
	KeepName bool
	call     func(ev *evaluator, n *Call, args []Value, ts int64) (Value, error)
}

var functions map[string]*function

// unsupportedFunctions are Prometheus functions TinyObs deliberately omits.
// Calling them is an explicit "not supported" error, not "unknown function".
var unsupportedFunctions = map[string]bool{
	"holt_winters": true, "double_exponential_smoothing": true,
	"histogram_avg": true, "histogram_count": true, "histogram_sum": true, "histogram_fraction": true,
	"histogram_stddev": true, "histogram_stdvar": true, "info": true, "mad_over_time": true,
	"sort_by_label": true, "sort_by_label_desc": true, "first_over_time": true,
	"ts_of_min_over_time": true, "ts_of_max_over_time": true, "ts_of_last_over_time": true,
	"ts_of_first_over_time": true, "limitk": true, "limit_ratio": true,
}

func init() {
	fs := []*function{
		// Range vector functions.
		{Name: "rate", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, w window, ts int64) (float64, bool) {
			return extrapolatedRate(pts, w, ts, true, true)
		})},
		{Name: "increase", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, w window, ts int64) (float64, bool) {
			return extrapolatedRate(pts, w, ts, true, false)
		})},
		{Name: "delta", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, w window, ts int64) (float64, bool) {
			return extrapolatedRate(pts, w, ts, false, false)
		})},
		{Name: "irate", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, _ window, _ int64) (float64, bool) {
			return instantValue(pts, true)
		})},
		{Name: "idelta", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, _ window, _ int64) (float64, bool) {
			return instantValue(pts, false)
		})},
		{Name: "changes", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, _ window, _ int64) (float64, bool) {
			n := 0
			for i := 1; i < len(pts); i++ {
				a, b := pts[i-1].F, pts[i].F
				if a != b && !(math.IsNaN(a) && math.IsNaN(b)) {
					n++
				}
			}
			return float64(n), true
		})},
		{Name: "resets", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, _ window, _ int64) (float64, bool) {
			n := 0
			for i := 1; i < len(pts); i++ {
				if pts[i].F < pts[i-1].F {
					n++
				}
			}
			return float64(n), true
		})},
		{Name: "avg_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(mean)},
		{Name: "sum_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(kahanSum)},
		{Name: "count_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(func(v []float64) float64 { return float64(len(v)) })},
		{Name: "present_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(func([]float64) float64 { return 1 })},
		{Name: "last_over_time", ArgTypes: []ValueType{TypeMatrix}, KeepName: true, call: overValues(func(v []float64) float64 { return v[len(v)-1] })},
		{Name: "min_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(func(v []float64) float64 {
			r := v[0]
			for _, x := range v[1:] {
				if x < r || math.IsNaN(r) {
					r = x
				}
			}
			return r
		})},
		{Name: "max_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(func(v []float64) float64 {
			r := v[0]
			for _, x := range v[1:] {
				if x > r || math.IsNaN(r) {
					r = x
				}
			}
			return r
		})},
		{Name: "stddev_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(func(v []float64) float64 { return math.Sqrt(variance(v)) })},
		{Name: "stdvar_over_time", ArgTypes: []ValueType{TypeMatrix}, call: overValues(variance)},
		{Name: "deriv", ArgTypes: []ValueType{TypeMatrix}, call: overSeries(func(pts []Point, _ window, _ int64) (float64, bool) {
			if len(pts) < 2 {
				return 0, false
			}
			slope, _ := linearRegression(pts, pts[0].T)
			return slope, true
		})},
		{Name: "predict_linear", ArgTypes: []ValueType{TypeMatrix, TypeScalar}, call: funcPredictLinear},
		{Name: "quantile_over_time", ArgTypes: []ValueType{TypeScalar, TypeMatrix}, call: funcQuantileOverTime},
		{Name: "absent_over_time", ArgTypes: []ValueType{TypeMatrix}, call: funcAbsentOverTime},

		// Instant vector functions.
		{Name: "abs", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Abs)},
		{Name: "ceil", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Ceil)},
		{Name: "floor", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Floor)},
		{Name: "exp", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Exp)},
		{Name: "sqrt", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Sqrt)},
		{Name: "ln", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Log)},
		{Name: "log2", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Log2)},
		{Name: "log10", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Log10)},
		{Name: "sgn", ArgTypes: []ValueType{TypeVector}, call: mathFunc(func(v float64) float64 {
			switch {
			case v < 0:
				return -1
			case v > 0:
				return 1
			}
			return v // 0 or NaN
		})},
		{Name: "deg", ArgTypes: []ValueType{TypeVector}, call: mathFunc(func(v float64) float64 { return v * 180 / math.Pi })},
		{Name: "rad", ArgTypes: []ValueType{TypeVector}, call: mathFunc(func(v float64) float64 { return v * math.Pi / 180 })},
		{Name: "sin", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Sin)},
		{Name: "cos", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Cos)},
		{Name: "tan", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Tan)},
		{Name: "asin", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Asin)},
		{Name: "acos", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Acos)},
		{Name: "atan", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Atan)},
		{Name: "sinh", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Sinh)},
		{Name: "cosh", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Cosh)},
		{Name: "tanh", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Tanh)},
		{Name: "asinh", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Asinh)},
		{Name: "acosh", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Acosh)},
		{Name: "atanh", ArgTypes: []ValueType{TypeVector}, call: mathFunc(math.Atanh)},
		{Name: "round", ArgTypes: []ValueType{TypeVector, TypeScalar}, Optional: 1, call: funcRound},
		{Name: "clamp", ArgTypes: []ValueType{TypeVector, TypeScalar, TypeScalar}, call: funcClamp},
		{Name: "clamp_min", ArgTypes: []ValueType{TypeVector, TypeScalar}, call: funcClamp},
		{Name: "clamp_max", ArgTypes: []ValueType{TypeVector, TypeScalar}, call: funcClamp},
		{Name: "histogram_quantile", ArgTypes: []ValueType{TypeScalar, TypeVector}, call: funcHistogramQuantile},
		{Name: "timestamp", ArgTypes: []ValueType{TypeVector}, call: funcTimestamp},
		{Name: "absent", ArgTypes: []ValueType{TypeVector}, call: funcAbsent},
		{Name: "label_replace", ArgTypes: []ValueType{TypeVector, TypeString, TypeString, TypeString, TypeString}, KeepName: true, call: funcLabelReplace},
		{Name: "label_join", ArgTypes: []ValueType{TypeVector, TypeString, TypeString, TypeString}, Optional: 1, Variadic: true, KeepName: true, call: funcLabelJoin},
		{Name: "sort", ArgTypes: []ValueType{TypeVector}, KeepName: true, call: funcSort(false)},
		{Name: "sort_desc", ArgTypes: []ValueType{TypeVector}, KeepName: true, call: funcSort(true)},

		// Scalars and conversions.
		{Name: "time", ReturnType: TypeScalar, call: func(_ *evaluator, _ *Call, _ []Value, ts int64) (Value, error) {
			return Scalar{T: ts, V: float64(ts) / 1000}, nil
		}},
		{Name: "pi", ReturnType: TypeScalar, call: func(_ *evaluator, _ *Call, _ []Value, ts int64) (Value, error) {
			return Scalar{T: ts, V: math.Pi}, nil
		}},
		{Name: "scalar", ArgTypes: []ValueType{TypeVector}, ReturnType: TypeScalar, call: func(_ *evaluator, _ *Call, args []Value, ts int64) (Value, error) {
			v := args[0].(Vector)
			if len(v) != 1 {
				return Scalar{T: ts, V: math.NaN()}, nil
			}
			return Scalar{T: ts, V: v[0].F}, nil
		}},
		{Name: "vector", ArgTypes: []ValueType{TypeScalar}, call: func(_ *evaluator, _ *Call, args []Value, ts int64) (Value, error) {
			return Vector{{Metric: labels.Labels{}, T: ts, F: args[0].(Scalar).V}}, nil
		}},
	}

	// Date functions take an optional vector of Unix timestamps (default: now).
	dateFuncs := map[string]func(time.Time) float64{
		"minute":       func(t time.Time) float64 { return float64(t.Minute()) },
		"hour":         func(t time.Time) float64 { return float64(t.Hour()) },
		"day_of_week":  func(t time.Time) float64 { return float64(t.Weekday()) },
		"day_of_month": func(t time.Time) float64 { return float64(t.Day()) },
		"day_of_year":  func(t time.Time) float64 { return float64(t.YearDay()) },
		"days_in_month": func(t time.Time) float64 {
			return float64(32 - time.Date(t.Year(), t.Month(), 32, 0, 0, 0, 0, time.UTC).Day())
		},
		"month": func(t time.Time) float64 { return float64(t.Month()) },
		"year":  func(t time.Time) float64 { return float64(t.Year()) },
	}
	for name, f := range dateFuncs {
		fs = append(fs, &function{Name: name, ArgTypes: []ValueType{TypeVector}, Optional: 1, call: dateFunc(f)})
	}

	functions = map[string]*function{}
	for _, f := range fs {
		if f.ReturnType == "" {
			f.ReturnType = TypeVector
		}
		functions[f.Name] = f
	}
}

func (ev *evaluator) evalCall(n *Call, ts int64) (Value, error) {
	args := make([]Value, len(n.Args))
	for i, a := range n.Args {
		v, err := ev.eval(a, ts)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	v, err := n.Func.call(ev, n, args, ts)
	if err != nil {
		return nil, err
	}
	if vec, ok := v.(Vector); ok {
		for i := range vec {
			vec[i].T = ts
			if !n.Func.KeepName {
				vec[i].DropName = true
			}
		}
	}
	return v, nil
}

// window is the time range a range-vector argument covers at evaluation time ts:
// (ts-offset-range, ts-offset].
type window struct {
	Range, Offset time.Duration
}

// argWindow returns the window of a matrix selector or subquery argument.
func argWindow(e Expr) window {
	for {
		switch n := e.(type) {
		case *ParenExpr:
			e = n.Expr
		case *MatrixSelector:
			return window{n.Range, n.VectorSelector.Offset}
		case *SubqueryExpr:
			return window{n.Range, n.Offset}
		default:
			return window{}
		}
	}
}

// overSeries applies f to each series of a range vector argument.
func overSeries(f func(pts []Point, w window, ts int64) (float64, bool)) func(*evaluator, *Call, []Value, int64) (Value, error) {
	return func(_ *evaluator, n *Call, args []Value, ts int64) (Value, error) {
		ms := argWindow(n.Args[len(n.Args)-1])
		var out Vector
		for _, s := range args[len(args)-1].(Matrix) {
			if v, ok := f(s.Points, ms, ts); ok {
				out = append(out, Sample{Metric: s.Metric, F: v})
			}
		}
		return out, nil
	}
}

func overValues(f func([]float64) float64) func(*evaluator, *Call, []Value, int64) (Value, error) {
	return overSeries(func(pts []Point, _ window, _ int64) (float64, bool) {
		vals := make([]float64, len(pts))
		for i, p := range pts {
			vals[i] = p.F
		}
		return f(vals), true
	})
}

// extrapolatedRate implements rate, increase and delta. The change across the
// window is extrapolated to the window edges when the first and last samples
// are close to them, because samples rarely fall exactly on the boundaries.
// For counters, drops in value are treated as resets, and extrapolation never
// goes below zero.
func extrapolatedRate(pts []Point, w window, ts int64, isCounter, isRate bool) (float64, bool) {
	if len(pts) < 2 {
		return 0, false
	}
	rangeEnd := ts - w.Offset.Milliseconds()
	rangeStart := rangeEnd - w.Range.Milliseconds()
	first, last := pts[0], pts[len(pts)-1]

	result := last.F - first.F
	if isCounter {
		prev := 0.0
		for _, p := range pts {
			if p.F < prev {
				result += prev
			}
			prev = p.F
		}
	}

	durationToStart := float64(first.T-rangeStart) / 1000
	durationToEnd := float64(rangeEnd-last.T) / 1000
	sampledInterval := float64(last.T-first.T) / 1000
	avgInterval := sampledInterval / float64(len(pts)-1)
	threshold := avgInterval * 1.1

	if durationToStart >= threshold {
		durationToStart = avgInterval / 2
	}
	if isCounter && result > 0 && first.F >= 0 {
		// Don't extrapolate to before the counter would have been zero.
		if durationToZero := sampledInterval * (first.F / result); durationToZero < durationToStart {
			durationToStart = durationToZero
		}
	}
	if durationToEnd >= threshold {
		durationToEnd = avgInterval / 2
	}

	factor := (sampledInterval + durationToStart + durationToEnd) / sampledInterval
	if isRate {
		factor /= w.Range.Seconds()
	}
	return result * factor, true
}

// instantValue implements irate and idelta from the last two samples.
func instantValue(pts []Point, isRate bool) (float64, bool) {
	if len(pts) < 2 {
		return 0, false
	}
	last, prev := pts[len(pts)-1], pts[len(pts)-2]
	var v float64
	if isRate && last.F < prev.F {
		v = last.F // counter reset
	} else {
		v = last.F - prev.F
	}
	interval := last.T - prev.T
	if interval == 0 {
		return 0, false
	}
	if isRate {
		v /= float64(interval) / 1000
	}
	return v, true
}

// linearRegression fits a least-squares line to pts, with x measured in
// seconds from interceptTime. A constant series has slope 0.
func linearRegression(pts []Point, interceptTime int64) (slope, intercept float64) {
	var n, sumX, cX, sumY, cY, sumXY, cXY, sumX2, cX2 float64
	constY := true
	for i, p := range pts {
		if i > 0 && p.F != pts[0].F {
			constY = false
		}
		n++
		x := float64(p.T-interceptTime) / 1000
		sumX, cX = kahanSumInc(x, sumX, cX)
		sumY, cY = kahanSumInc(p.F, sumY, cY)
		sumXY, cXY = kahanSumInc(x*p.F, sumXY, cXY)
		sumX2, cX2 = kahanSumInc(x*x, sumX2, cX2)
	}
	if constY {
		if math.IsInf(pts[0].F, 0) {
			return math.NaN(), math.NaN()
		}
		return 0, pts[0].F
	}
	sumX, sumY, sumXY, sumX2 = sumX+cX, sumY+cY, sumXY+cXY, sumX2+cX2
	covXY := sumXY - sumX*sumY/n
	varX := sumX2 - sumX*sumX/n
	slope = covXY / varX
	intercept = sumY/n - slope*sumX/n
	return slope, intercept
}

// funcPredictLinear extrapolates a series' linear trend by the given number
// of seconds past the evaluation time.
func funcPredictLinear(_ *evaluator, _ *Call, args []Value, ts int64) (Value, error) {
	dur := args[1].(Scalar).V
	var out Vector
	for _, s := range args[0].(Matrix) {
		if len(s.Points) < 2 {
			continue
		}
		slope, intercept := linearRegression(s.Points, ts)
		out = append(out, Sample{Metric: s.Metric, F: slope*dur + intercept})
	}
	return out, nil
}

func funcQuantileOverTime(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
	q := args[0].(Scalar).V
	var out Vector
	for _, s := range args[1].(Matrix) {
		vals := make([]float64, len(s.Points))
		for i, p := range s.Points {
			vals[i] = p.F
		}
		out = append(out, Sample{Metric: s.Metric, F: quantile(q, vals)})
	}
	return out, nil
}

func mathFunc(f func(float64) float64) func(*evaluator, *Call, []Value, int64) (Value, error) {
	return func(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
		in := args[0].(Vector)
		out := make(Vector, len(in))
		for i, s := range in {
			out[i] = Sample{Metric: s.Metric, F: f(s.F), DropName: s.DropName}
		}
		return out, nil
	}
}

func funcRound(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
	toNearest := 1.0
	if len(args) > 1 {
		toNearest = args[1].(Scalar).V
	}
	inv := 1.0 / toNearest // division first, as in Prometheus, to limit float error
	in := args[0].(Vector)
	out := make(Vector, len(in))
	for i, s := range in {
		out[i] = Sample{Metric: s.Metric, F: math.Floor(s.F*inv+0.5) / inv}
	}
	return out, nil
}

func funcClamp(_ *evaluator, n *Call, args []Value, _ int64) (Value, error) {
	lo, hi := math.Inf(-1), math.Inf(1)
	switch n.Func.Name {
	case "clamp":
		lo, hi = args[1].(Scalar).V, args[2].(Scalar).V
		if hi < lo {
			return Vector{}, nil
		}
	case "clamp_min":
		lo = args[1].(Scalar).V
	case "clamp_max":
		hi = args[1].(Scalar).V
	}
	in := args[0].(Vector)
	out := make(Vector, len(in))
	for i, s := range in {
		out[i] = Sample{Metric: s.Metric, F: math.Max(lo, math.Min(hi, s.F))}
	}
	return out, nil
}

func funcTimestamp(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
	in := args[0].(Vector)
	out := make(Vector, len(in))
	for i, s := range in {
		// Selector results carry the sample's own timestamp; other
		// expressions carry the evaluation time.
		out[i] = Sample{Metric: s.Metric, F: float64(s.T) / 1000}
	}
	return out, nil
}

// absentLabels returns the labels an absent() result carries: those fixed by
// equality matchers in the selector.
func absentLabels(e Expr) labels.Labels {
	for {
		switch n := e.(type) {
		case *ParenExpr:
			e = n.Expr
			continue
		case *MatrixSelector:
			e = n.VectorSelector
			continue
		case *VectorSelector:
			seen := map[string]int{}
			var ls []labels.Label
			for _, m := range n.Matchers {
				if m.Name == labels.MetricName {
					continue
				}
				seen[m.Name]++
				if m.Type == labels.MatchEqual {
					ls = append(ls, labels.Label{Name: m.Name, Value: m.Value})
				}
			}
			var out []labels.Label
			for _, l := range ls {
				if seen[l.Name] == 1 { // ambiguous names are left out
					out = append(out, l)
				}
			}
			return labels.New(out...)
		}
		return labels.Labels{}
	}
}

func funcAbsent(_ *evaluator, n *Call, args []Value, _ int64) (Value, error) {
	if len(args[0].(Vector)) > 0 {
		return Vector{}, nil
	}
	return Vector{{Metric: absentLabels(n.Args[0]), F: 1}}, nil
}

func funcAbsentOverTime(_ *evaluator, n *Call, args []Value, _ int64) (Value, error) {
	if len(args[0].(Matrix)) > 0 {
		return Vector{}, nil
	}
	return Vector{{Metric: absentLabels(n.Args[0]), F: 1}}, nil
}

func validLabelName(s string) bool { return s != "" && utf8.ValidString(s) }

func funcLabelReplace(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
	dst := args[1].(String).V
	repl := args[2].(String).V
	src := args[3].(String).V
	pattern := args[4].(String).V
	re, err := regexp.Compile("^(?s:" + pattern + ")$")
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression in label_replace(): %s", pattern)
	}
	if !validLabelName(dst) {
		return nil, fmt.Errorf("invalid destination label name in label_replace(): %s", dst)
	}
	in := args[0].(Vector)
	out := make(Vector, len(in))
	for i, s := range in {
		m := s.Metric
		v := m.Get(src)
		if idx := re.FindStringSubmatchIndex(v); idx != nil {
			res := re.ExpandString(nil, repl, v, idx)
			m = m.Set(dst, string(res))
		}
		// Writing __name__ explicitly keeps it.
		out[i] = Sample{Metric: m, T: s.T, F: s.F, DropName: s.DropName && dst != labels.MetricName}
	}
	return out, nil
}

func funcLabelJoin(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
	dst := args[1].(String).V
	sep := args[2].(String).V
	var srcs []string
	for _, a := range args[3:] {
		srcs = append(srcs, a.(String).V)
	}
	for _, s := range srcs {
		if !validLabelName(s) {
			return nil, fmt.Errorf("invalid source label name in label_join(): %s", s)
		}
	}
	if !validLabelName(dst) {
		return nil, fmt.Errorf("invalid destination label name in label_join(): %s", dst)
	}
	in := args[0].(Vector)
	out := make(Vector, len(in))
	for i, s := range in {
		vals := make([]string, len(srcs))
		for j, src := range srcs {
			vals[j] = s.Metric.Get(src)
		}
		out[i] = Sample{Metric: s.Metric.Set(dst, strings.Join(vals, sep)), T: s.T, F: s.F, DropName: s.DropName && dst != labels.MetricName}
	}
	return out, nil
}

func funcSort(desc bool) func(*evaluator, *Call, []Value, int64) (Value, error) {
	return func(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
		out := append(Vector(nil), args[0].(Vector)...)
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].F, out[j].F
			if math.IsNaN(a) {
				return false
			}
			if math.IsNaN(b) {
				return true
			}
			if desc {
				return a > b
			}
			return a < b
		})
		return out, nil
	}
}

func dateFunc(f func(time.Time) float64) func(*evaluator, *Call, []Value, int64) (Value, error) {
	return func(_ *evaluator, _ *Call, args []Value, ts int64) (Value, error) {
		if len(args) == 0 {
			return Vector{{Metric: labels.Labels{}, F: f(time.UnixMilli(ts).UTC())}}, nil
		}
		in := args[0].(Vector)
		out := make(Vector, len(in))
		for i, s := range in {
			sec := s.F
			if math.IsNaN(sec) || math.IsInf(sec, 0) {
				out[i] = Sample{Metric: s.Metric, F: math.NaN()}
				continue
			}
			out[i] = Sample{Metric: s.Metric, F: f(time.Unix(int64(sec), 0).UTC())}
		}
		return out, nil
	}
}

// Classic histogram quantiles.

type bucket struct {
	upper float64
	count float64
}

func funcHistogramQuantile(_ *evaluator, _ *Call, args []Value, _ int64) (Value, error) {
	q := args[0].(Scalar).V
	type group struct {
		metric  labels.Labels
		buckets []bucket
	}
	groups := map[string]*group{}
	var order []*group
	for _, s := range args[1].(Vector) {
		le, err := strconv.ParseFloat(s.Metric.Get("le"), 64)
		if err != nil || s.Metric.Get("le") == "" {
			continue // not a bucket series
		}
		m := s.Metric.Without("le")
		k := labelsKey(m)
		g := groups[k]
		if g == nil {
			g = &group{metric: m}
			groups[k] = g
			order = append(order, g)
		}
		g.buckets = append(g.buckets, bucket{le, s.F})
	}
	var out Vector
	for _, g := range order {
		out = append(out, Sample{Metric: g.metric, F: bucketQuantile(q, g.buckets)})
	}
	return out, nil
}

// bucketQuantile estimates a quantile from cumulative buckets by linear
// interpolation within the bucket that contains the target rank.
func bucketQuantile(q float64, buckets []bucket) float64 {
	if math.IsNaN(q) {
		return math.NaN()
	}
	if q < 0 {
		return math.Inf(-1)
	}
	if q > 1 {
		return math.Inf(1)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].upper < buckets[j].upper })
	if len(buckets) == 0 || !math.IsInf(buckets[len(buckets)-1].upper, 1) {
		return math.NaN()
	}
	// Merge buckets with the same upper bound.
	merged := buckets[:1]
	for _, b := range buckets[1:] {
		if b.upper == merged[len(merged)-1].upper {
			merged[len(merged)-1].count += b.count
		} else {
			merged = append(merged, b)
		}
	}
	buckets = merged
	ensureMonotonic(buckets)
	if len(buckets) < 2 {
		return math.NaN()
	}
	total := buckets[len(buckets)-1].count
	if total == 0 {
		return math.NaN()
	}
	rank := q * total
	b := sort.Search(len(buckets)-1, func(i int) bool { return buckets[i].count >= rank })
	if b == len(buckets)-1 {
		return buckets[len(buckets)-2].upper
	}
	if b == 0 && buckets[0].upper <= 0 {
		return buckets[0].upper
	}
	var start float64
	end := buckets[b].upper
	count := buckets[b].count
	if b > 0 {
		start = buckets[b-1].upper
		count -= buckets[b-1].count
		rank -= buckets[b-1].count
	}
	return start + (end-start)*(rank/count)
}

// ensureMonotonic makes bucket counts non-decreasing. Counts can dip because
// buckets are scraped non-atomically, or because of float rounding.
func ensureMonotonic(buckets []bucket) {
	prev := buckets[0].count
	for i := 1; i < len(buckets); i++ {
		cur := buckets[i].count
		switch {
		case cur == prev:
		case almostEqual(prev, cur, 1e-12) || cur < prev:
			buckets[i].count = prev
		default:
			prev = cur
		}
	}
}

func almostEqual(a, b, epsilon float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	if a == 0 || b == 0 || diff < math.SmallestNonzeroFloat64 {
		return diff < epsilon*math.SmallestNonzeroFloat64
	}
	return diff/math.Min(math.Abs(a)+math.Abs(b), math.MaxFloat64) < epsilon
}
