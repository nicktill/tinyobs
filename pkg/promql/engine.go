package promql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// Storage is what the engine reads from. *tsdb.DB implements it.
type Storage interface {
	Select(ctx context.Context, mint, maxt int64, maxSamples int, ms ...*labels.Matcher) ([]tsdb.Series, error)
}

// EngineOptions sets query limits.
type EngineOptions struct {
	// LookbackDelta is how far back an instant selector looks for a sample.
	LookbackDelta time.Duration
	// MaxSamples bounds the samples a single query may load from storage.
	MaxSamples int
	// Timeout bounds query evaluation.
	Timeout time.Duration
	// MaxPoints bounds the points per series in a range query result.
	MaxPoints int
}

// DefaultEngineOptions matches Prometheus's defaults for lookback and points,
// with a sample limit suited to a single-machine server.
func DefaultEngineOptions() EngineOptions {
	return EngineOptions{
		LookbackDelta: 5 * time.Minute,
		MaxSamples:    5_000_000,
		Timeout:       30 * time.Second,
		MaxPoints:     11_000,
	}
}

// Engine evaluates PromQL queries. It is safe for concurrent use.
type Engine struct {
	db   Storage
	opts EngineOptions
}

// NewEngine returns an engine reading from db.
func NewEngine(db Storage, opts EngineOptions) *Engine {
	d := DefaultEngineOptions()
	if opts.LookbackDelta <= 0 {
		opts.LookbackDelta = d.LookbackDelta
	}
	if opts.MaxSamples <= 0 {
		opts.MaxSamples = d.MaxSamples
	}
	if opts.Timeout <= 0 {
		opts.Timeout = d.Timeout
	}
	if opts.MaxPoints <= 0 {
		opts.MaxPoints = d.MaxPoints
	}
	return &Engine{db: db, opts: opts}
}

// ErrTimeout is returned when evaluation exceeds the timeout.
var ErrTimeout = errors.New("query timed out")

// Instant evaluates q at a single time.
func (e *Engine) Instant(ctx context.Context, q string, ts time.Time) (Value, error) {
	expr, err := Parse(q)
	if err != nil {
		return nil, err
	}
	t := ts.UnixMilli()
	ev, cancel, err := e.newEvaluator(ctx, expr, t, t, 0)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return ev.run(func() (Value, error) {
		v, err := ev.eval(expr, t)
		if err != nil {
			return nil, err
		}
		if vec, ok := v.(Vector); ok {
			for i := range vec {
				vec[i].T = t
			}
			vec = finalizeNames(vec)
			if err := checkDuplicates(vec); err != nil {
				return nil, err
			}
			return vec, nil
		}
		if s, ok := v.(String); ok {
			s.T = t
			return s, nil
		}
		return v, nil
	})
}

// Range evaluates q at each step from start to end inclusive.
func (e *Engine) Range(ctx context.Context, q string, start, end time.Time, step time.Duration) (Matrix, error) {
	if step <= 0 {
		return nil, fmt.Errorf("zero or negative query resolution step widths are not accepted")
	}
	if end.Before(start) {
		return nil, fmt.Errorf("end timestamp must not be before start time")
	}
	if n := end.Sub(start)/step + 1; int(n) > e.opts.MaxPoints || n < 0 {
		return nil, fmt.Errorf("exceeded maximum resolution of %d points per timeseries; try decreasing the query resolution (step)", e.opts.MaxPoints)
	}
	expr, err := Parse(q)
	if err != nil {
		return nil, err
	}
	if t := expr.Type(); t != TypeScalar && t != TypeVector {
		return nil, fmt.Errorf("invalid expression type %q for range query, must be Scalar or instant Vector", typeName(t))
	}
	s, en, st := start.UnixMilli(), end.UnixMilli(), step.Milliseconds()
	ev, cancel, err := e.newEvaluator(ctx, expr, s, en, st)
	if err != nil {
		return nil, err
	}
	defer cancel()
	v, err := ev.run(func() (Value, error) {
		byKey := map[string]*Series{}
		var order []*Series
		for ts := s; ts <= en; ts += st {
			v, err := ev.eval(expr, ts)
			if err != nil {
				return nil, err
			}
			var vec Vector
			switch x := v.(type) {
			case Scalar:
				vec = Vector{{Metric: labels.Labels{}, F: x.V}}
			case Vector:
				vec = finalizeNames(x)
				if err := checkDuplicates(vec); err != nil {
					return nil, err
				}
			}
			for _, smp := range vec {
				k := labelsKey(smp.Metric)
				ser := byKey[k]
				if ser == nil {
					ser = &Series{Metric: smp.Metric}
					byKey[k] = ser
					order = append(order, ser)
				}
				ser.Points = append(ser.Points, Point{T: ts, F: smp.F})
			}
		}
		m := make(Matrix, 0, len(order))
		for _, s := range order {
			m = append(m, *s)
		}
		sortMatrix(m)
		return m, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(Matrix), nil
}

// evaluator holds the data for one query.
type evaluator struct {
	ctx  context.Context
	opts EngineOptions
	data map[*VectorSelector][]tsdb.Series
	// ranges holds, for range selectors, each series' samples as points with
	// staleness markers removed, so every step can take a sub-slice.
	ranges   map[*VectorSelector][]Series
	startT   int64
	endT     int64
	interval int64
}

func (e *Engine) newEvaluator(ctx context.Context, expr Expr, start, end, step int64) (*evaluator, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, e.opts.Timeout)
	ev := &evaluator{ctx: ctx, opts: e.opts, data: map[*VectorSelector][]tsdb.Series{}, ranges: map[*VectorSelector][]Series{}, startT: start, endT: end, interval: step}

	// Load every selector's data once, for the widest window any step needs.
	remaining := e.opts.MaxSamples
	if err := e.prefetch(ev, expr, start, end, &remaining); err != nil {
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			err = ErrTimeout
		}
		return nil, nil, err
	}
	return ev, cancel, nil
}

// prefetch loads the data each selector needs for evaluation times in
// [start, end]. Subqueries widen the window of the selectors beneath them.
func (e *Engine) prefetch(ev *evaluator, n Expr, start, end int64, remaining *int) error {
	load := func(vs *VectorSelector, window int64) error {
		off := vs.Offset.Milliseconds()
		series, err := e.db.Select(ev.ctx, start-off-window+1, end-off, *remaining, vs.Matchers...)
		if err != nil {
			if errors.Is(err, tsdb.ErrSampleLimit) {
				return fmt.Errorf("query processing would load too many samples into memory (limit %d)", e.opts.MaxSamples)
			}
			return err
		}
		for _, s := range series {
			*remaining -= len(s.Samples)
		}
		ev.data[vs] = series
		return nil
	}
	switch x := n.(type) {
	case *VectorSelector:
		return load(x, e.opts.LookbackDelta.Milliseconds())
	case *MatrixSelector:
		if err := load(x.VectorSelector, x.Range.Milliseconds()); err != nil {
			return err
		}
		var rs []Series
		for _, s := range ev.data[x.VectorSelector] {
			pts := make([]Point, 0, len(s.Samples))
			for _, smp := range s.Samples {
				if !tsdb.IsStaleNaN(smp.V) {
					pts = append(pts, Point{smp.T, smp.V})
				}
			}
			rs = append(rs, Series{Metric: s.Labels, Points: pts})
		}
		ev.ranges[x.VectorSelector] = rs
		delete(ev.data, x.VectorSelector)
		return nil
	case *SubqueryExpr:
		off := x.Offset.Milliseconds()
		return e.prefetch(ev, x.Expr, start-off-x.Range.Milliseconds(), end-off, remaining)
	case *ParenExpr:
		return e.prefetch(ev, x.Expr, start, end, remaining)
	case *UnaryExpr:
		return e.prefetch(ev, x.Expr, start, end, remaining)
	case *BinaryExpr:
		if err := e.prefetch(ev, x.LHS, start, end, remaining); err != nil {
			return err
		}
		return e.prefetch(ev, x.RHS, start, end, remaining)
	case *AggregateExpr:
		if x.Param != nil {
			if err := e.prefetch(ev, x.Param, start, end, remaining); err != nil {
				return err
			}
		}
		return e.prefetch(ev, x.Expr, start, end, remaining)
	case *Call:
		for _, a := range x.Args {
			if err := e.prefetch(ev, a, start, end, remaining); err != nil {
				return err
			}
		}
	}
	return nil
}

// DefaultSubqueryStep is the step of a subquery that doesn't specify one,
// matching Prometheus's default evaluation interval.
const DefaultSubqueryStep = time.Minute

// evalSubquery evaluates the inner expression at each step-aligned time in
// (ts-offset-range, ts-offset].
func (ev *evaluator) evalSubquery(n *SubqueryExpr, ts int64) (Value, error) {
	step := n.Step.Milliseconds()
	if step <= 0 {
		step = DefaultSubqueryStep.Milliseconds()
	}
	ref := ts - n.Offset.Milliseconds()
	start := ref - n.Range.Milliseconds()
	first := floorDiv(start, step) * step
	if first <= start {
		first += step
	}
	byKey := map[string]*Series{}
	var order []*Series
	for t := first; t <= ref; t += step {
		v, err := ev.eval(n.Expr, t)
		if err != nil {
			return nil, err
		}
		vec := finalizeNames(v.(Vector))
		if err := checkDuplicates(vec); err != nil {
			return nil, err
		}
		for _, s := range vec {
			k := labelsKey(s.Metric)
			ser := byKey[k]
			if ser == nil {
				ser = &Series{Metric: s.Metric}
				byKey[k] = ser
				order = append(order, ser)
			}
			ser.Points = append(ser.Points, Point{T: t, F: s.F})
		}
	}
	m := make(Matrix, len(order))
	for i, s := range order {
		m[i] = *s
	}
	return m, nil
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func (ev *evaluator) run(f func() (Value, error)) (Value, error) {
	v, err := f()
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrTimeout
	}
	return v, err
}

func (ev *evaluator) errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// eval evaluates e at time ts.
func (ev *evaluator) eval(e Expr, ts int64) (Value, error) {
	if err := ev.ctx.Err(); err != nil {
		return nil, err
	}
	switch n := e.(type) {
	case *NumberLiteral:
		return Scalar{T: ts, V: n.Val}, nil
	case *StringLiteral:
		return String{T: ts, V: n.Val}, nil
	case *ParenExpr:
		return ev.eval(n.Expr, ts)
	case *VectorSelector:
		return ev.selectInstant(n, ts), nil
	case *MatrixSelector:
		return ev.selectRange(n, ts), nil
	case *SubqueryExpr:
		return ev.evalSubquery(n, ts)
	case *UnaryExpr:
		v, err := ev.eval(n.Expr, ts)
		if err != nil || n.Op == "+" {
			return v, err
		}
		switch x := v.(type) {
		case Scalar:
			return Scalar{T: ts, V: -x.V}, nil
		case Vector:
			out := make(Vector, len(x))
			for i, s := range x {
				out[i] = Sample{Metric: s.Metric, T: ts, F: -s.F, DropName: true}
			}
			return out, nil
		}
	case *BinaryExpr:
		return ev.evalBinary(n, ts)
	case *AggregateExpr:
		return ev.evalAggregate(n, ts)
	case *Call:
		return ev.evalCall(n, ts)
	}
	return nil, ev.errorf("unhandled expression %T", e)
}

// selectInstant returns, for each series, the newest sample in (ts-lookback, ts],
// unless it is a staleness marker. Samples keep their own timestamp so that
// timestamp() can read it; everything downstream uses the evaluation time.
func (ev *evaluator) selectInstant(vs *VectorSelector, ts int64) Vector {
	ref := ts - vs.Offset.Milliseconds()
	mint := ref - ev.opts.LookbackDelta.Milliseconds()
	var out Vector
	for _, s := range ev.data[vs] {
		i := sort.Search(len(s.Samples), func(i int) bool { return s.Samples[i].T > ref }) - 1
		if i < 0 || s.Samples[i].T <= mint || tsdb.IsStaleNaN(s.Samples[i].V) {
			continue
		}
		out = append(out, Sample{Metric: s.Labels, T: s.Samples[i].T, F: s.Samples[i].V})
	}
	return out
}

// selectRange returns, for each series, the samples in (ts-range, ts],
// excluding staleness markers. Series with no samples are omitted. The points
// are sub-slices of the loaded data and must not be modified.
func (ev *evaluator) selectRange(ms *MatrixSelector, ts int64) Matrix {
	ref := ts - ms.VectorSelector.Offset.Milliseconds()
	mint := ref - ms.Range.Milliseconds()
	all := ev.ranges[ms.VectorSelector]
	out := make(Matrix, 0, len(all))
	for _, s := range all {
		pts := s.Points
		lo := sort.Search(len(pts), func(i int) bool { return pts[i].T > mint })
		hi := lo + sort.Search(len(pts)-lo, func(i int) bool { return pts[lo+i].T > ref })
		if hi > lo {
			out = append(out, Series{Metric: s.Metric, Points: pts[lo:hi:hi]})
		}
	}
	return out
}

// finalizeNames removes metric names marked for dropping.
func finalizeNames(v Vector) Vector {
	for i := range v {
		if v[i].DropName {
			v[i].Metric = v[i].Metric.DropMetricName()
			v[i].DropName = false
		}
	}
	return v
}

func checkDuplicates(v Vector) error {
	if len(v) < 2 {
		return nil
	}
	seen := make(map[string]struct{}, len(v))
	for _, s := range v {
		k := labelsKey(s.Metric)
		if _, dup := seen[k]; dup {
			return fmt.Errorf("vector cannot contain metrics with the same labelset")
		}
		seen[k] = struct{}{}
	}
	return nil
}

// labelsKey is an exact, collision-free key for a label set.
func labelsKey(ls labels.Labels) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Name)
		b.WriteByte(0xff)
		b.WriteString(l.Value)
		b.WriteByte(0xff)
	}
	return b.String()
}

// Binary operators.

func (ev *evaluator) evalBinary(n *BinaryExpr, ts int64) (Value, error) {
	lv, err := ev.eval(n.LHS, ts)
	if err != nil {
		return nil, err
	}
	rv, err := ev.eval(n.RHS, ts)
	if err != nil {
		return nil, err
	}
	switch l := lv.(type) {
	case Scalar:
		switch r := rv.(type) {
		case Scalar:
			v, keep := binop(n.Op, l.V, r.V)
			if n.ReturnBool {
				v = boolVal(keep)
			}
			return Scalar{T: ts, V: v}, nil
		case Vector:
			return vectorScalarBinop(n, r, l.V, true, ts), nil
		}
	case Vector:
		switch r := rv.(type) {
		case Scalar:
			return vectorScalarBinop(n, l, r.V, false, ts), nil
		case Vector:
			var out Vector
			switch n.Op {
			case "and":
				out = vectorAnd(l, r, n.Matching)
			case "or":
				out = vectorOr(l, r, n.Matching)
			case "unless":
				out = vectorUnless(l, r, n.Matching)
			default:
				out, err = vectorBinop(n, l, r, ts)
				if err != nil {
					return nil, err
				}
			}
			for i := range out {
				out[i].T = ts
			}
			return out, nil
		}
	}
	return nil, ev.errorf("invalid operands for %q", n.Op)
}

func boolVal(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// binop applies op. For comparisons it returns the left value and whether the
// comparison holds; for arithmetic, the result and true.
func binop(op string, l, r float64) (float64, bool) {
	switch op {
	case "+":
		return l + r, true
	case "-":
		return l - r, true
	case "*":
		return l * r, true
	case "/":
		return l / r, true
	case "%":
		return math.Mod(l, r), true
	case "^":
		return math.Pow(l, r), true
	case "atan2":
		return math.Atan2(l, r), true
	case "==":
		return l, l == r
	case "!=":
		return l, l != r
	case ">":
		return l, l > r
	case "<":
		return l, l < r
	case ">=":
		return l, l >= r
	case "<=":
		return l, l <= r
	}
	panic("unknown operator " + op)
}

// dropsName reports whether op removes the metric name from its result.
func dropsName(op string) bool { return !isComparison(op) && !isSetOp(op) }

func vectorScalarBinop(n *BinaryExpr, vec Vector, scalar float64, scalarLeft bool, ts int64) Vector {
	var out Vector
	for _, s := range vec {
		l, r := s.F, scalar
		if scalarLeft {
			l, r = r, l
		}
		v, keep := binop(n.Op, l, r)
		// A comparison keeps the vector's value, whichever side it is on.
		if isComparison(n.Op) && scalarLeft {
			v = r
		}
		if n.ReturnBool {
			v, keep = boolVal(keep), true
		}
		if !keep {
			continue
		}
		out = append(out, Sample{Metric: s.Metric, T: ts, F: v, DropName: s.DropName || dropsName(n.Op) || n.ReturnBool})
	}
	return out
}

// signature is the key samples are matched on.
func signature(ls labels.Labels, m *VectorMatching) string {
	if m.On {
		return labelsKey(ls.Keep(m.Labels...))
	}
	return labelsKey(ls.Without(append([]string{labels.MetricName}, m.Labels...)...))
}

func vectorBinop(n *BinaryExpr, lhs, rhs Vector, ts int64) (Vector, error) {
	m := n.Matching
	if m.Card == CardOneToMany {
		lhs, rhs = rhs, lhs
	}
	// The "one" side must have unique signatures.
	one := make(map[string]Sample, len(rhs))
	for _, s := range rhs {
		sig := signature(s.Metric, m)
		if dup, ok := one[sig]; ok {
			side := "right"
			if m.Card == CardOneToMany {
				side = "left"
			}
			return nil, fmt.Errorf("found duplicate series for the match group %s on the %s hand-side of the operation: [%s, %s];many-to-many matching not allowed: matching labels must be unique on one side",
				matchGroupString(s.Metric, m), side, dup.Metric, s.Metric)
		}
		one[sig] = s
	}

	matched := map[string]bool{}
	inserted := map[string]map[string]bool{}
	var out Vector
	for _, ls := range lhs {
		sig := signature(ls.Metric, m)
		rs, ok := one[sig]
		if !ok {
			continue
		}
		fl, fr := ls.F, rs.F
		if m.Card == CardOneToMany {
			fl, fr = fr, fl
		}
		v, keep := binop(n.Op, fl, fr)
		if n.ReturnBool {
			v, keep = boolVal(keep), true
		}
		if !keep {
			continue
		}
		metric := resultMetric(ls.Metric, rs.Metric, m)
		if m.Card == CardOneToOne {
			if matched[sig] {
				return nil, fmt.Errorf("multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)")
			}
			matched[sig] = true
		} else {
			k := labelsKey(metric)
			if inserted[sig] == nil {
				inserted[sig] = map[string]bool{}
			}
			if inserted[sig][k] {
				return nil, fmt.Errorf("multiple matches for labels: grouping labels must ensure unique matches")
			}
			inserted[sig][k] = true
		}
		drop := dropsName(n.Op) || n.ReturnBool || ls.DropName
		out = append(out, Sample{Metric: metric, T: ts, F: v, DropName: drop})
	}
	return out, nil
}

func matchGroupString(ls labels.Labels, m *VectorMatching) string {
	if m.On {
		return ls.Keep(m.Labels...).String()
	}
	return ls.Without(append([]string{labels.MetricName}, m.Labels...)...).String()
}

func resultMetric(lhs, rhs labels.Labels, m *VectorMatching) labels.Labels {
	out := lhs
	if m.Card == CardOneToOne {
		if m.On {
			out = out.Keep(m.Labels...)
		} else {
			out = out.Without(m.Labels...)
		}
	}
	for _, name := range m.Include {
		out = out.Set(name, rhs.Get(name))
	}
	return out
}

func vectorAnd(lhs, rhs Vector, m *VectorMatching) Vector {
	sigs := map[string]bool{}
	for _, s := range rhs {
		sigs[signature(s.Metric, m)] = true
	}
	var out Vector
	for _, s := range lhs {
		if sigs[signature(s.Metric, m)] {
			out = append(out, s)
		}
	}
	return out
}

func vectorOr(lhs, rhs Vector, m *VectorMatching) Vector {
	sigs := map[string]bool{}
	out := append(Vector{}, lhs...)
	for _, s := range lhs {
		sigs[signature(s.Metric, m)] = true
	}
	for _, s := range rhs {
		if !sigs[signature(s.Metric, m)] {
			out = append(out, s)
		}
	}
	return out
}

func vectorUnless(lhs, rhs Vector, m *VectorMatching) Vector {
	sigs := map[string]bool{}
	for _, s := range rhs {
		sigs[signature(s.Metric, m)] = true
	}
	var out Vector
	for _, s := range lhs {
		if !sigs[signature(s.Metric, m)] {
			out = append(out, s)
		}
	}
	return out
}
