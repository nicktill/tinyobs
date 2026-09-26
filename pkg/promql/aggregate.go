package promql

import (
	"fmt"
	"math"
	"sort"

	"github.com/nicktill/tinyobs/pkg/labels"
)

func (ev *evaluator) evalAggregate(n *AggregateExpr, ts int64) (Value, error) {
	v, err := ev.eval(n.Expr, ts)
	if err != nil {
		return nil, err
	}
	vec := v.(Vector)

	var param float64
	if n.Param != nil {
		p, err := ev.eval(n.Param, ts)
		if err != nil {
			return nil, err
		}
		param = p.(Scalar).V
	}

	type group struct {
		metric  labels.Labels
		samples []Sample
	}
	inGroup := func(name string) bool {
		if n.Without {
			if name == labels.MetricName {
				return false
			}
			for _, g := range n.Grouping {
				if g == name {
					return false
				}
			}
			return true
		}
		for _, g := range n.Grouping {
			if g == name {
				return true
			}
		}
		return false
	}
	groups := map[uint64][]*group{}
	var order []*group
	for _, s := range vec {
		h := groupHash(s.Metric, inGroup)
		var g *group
		for _, cand := range groups[h] {
			if sameGroup(s.Metric, cand.metric, inGroup) {
				g = cand
				break
			}
		}
		if g == nil {
			var gm labels.Labels
			for _, l := range s.Metric {
				if inGroup(l.Name) {
					gm = append(gm, l)
				}
			}
			if gm == nil {
				gm = labels.Labels{}
			}
			g = &group{metric: gm}
			groups[h] = append(groups[h], g)
			order = append(order, g)
		}
		g.samples = append(g.samples, s)
	}

	var out Vector
	for _, g := range order {
		switch n.Op {
		case "topk", "bottomk":
			k, err := intParam(param)
			if err != nil {
				return nil, err
			}
			for _, s := range selectK(g.samples, k, n.Op == "topk") {
				out = append(out, Sample{Metric: s.Metric, T: ts, F: s.F, DropName: s.DropName})
			}
			continue
		}
		vals := make([]float64, len(g.samples))
		for i, s := range g.samples {
			vals[i] = s.F
		}
		var r float64
		switch n.Op {
		case "sum":
			r = kahanSum(vals)
		case "avg":
			r = mean(vals)
		case "count":
			r = float64(len(vals))
		case "group":
			r = 1
		case "min":
			r = vals[0]
			for _, x := range vals[1:] {
				if r > x || math.IsNaN(r) {
					r = x
				}
			}
		case "max":
			r = vals[0]
			for _, x := range vals[1:] {
				if r < x || math.IsNaN(r) {
					r = x
				}
			}
		case "stddev":
			r = math.Sqrt(variance(vals))
		case "stdvar":
			r = variance(vals)
		case "quantile":
			r = quantile(param, vals)
		default:
			return nil, fmt.Errorf("aggregation %q: %w", n.Op, ErrUnsupported)
		}
		drop := false
		for _, s := range g.samples {
			drop = drop || s.DropName
		}
		out = append(out, Sample{Metric: g.metric, T: ts, F: r, DropName: drop})
	}
	return out, nil
}

// groupHash hashes the labels of ls selected by keep, without allocating.
func groupHash(ls labels.Labels, keep func(string) bool) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	for _, l := range ls {
		if !keep(l.Name) {
			continue
		}
		for i := 0; i < len(l.Name); i++ {
			h = (h ^ uint64(l.Name[i])) * prime
		}
		h = (h ^ 0xff) * prime
		for i := 0; i < len(l.Value); i++ {
			h = (h ^ uint64(l.Value[i])) * prime
		}
		h = (h ^ 0xfe) * prime
	}
	return h
}

// sameGroup reports whether the labels of ls selected by keep equal group.
func sameGroup(ls, group labels.Labels, keep func(string) bool) bool {
	i := 0
	for _, l := range ls {
		if !keep(l.Name) {
			continue
		}
		if i >= len(group) || group[i] != l {
			return false
		}
		i++
	}
	return i == len(group)
}

func intParam(f float64) (int, error) {
	if math.IsNaN(f) {
		return 0, fmt.Errorf("parameter value is NaN")
	}
	if f >= math.MaxInt64 || f <= math.MinInt64 {
		return 0, fmt.Errorf("scalar value %v overflows int64", f)
	}
	return int(f), nil
}

// selectK returns the k largest (top) or smallest samples, ordered, with NaN
// values considered last.
func selectK(samples []Sample, k int, top bool) []Sample {
	if k < 1 {
		return nil
	}
	s := append([]Sample(nil), samples...)
	sort.SliceStable(s, func(i, j int) bool {
		a, b := s[i].F, s[j].F
		if math.IsNaN(a) {
			return false
		}
		if math.IsNaN(b) {
			return true
		}
		if top {
			return a > b
		}
		return a < b
	})
	if k < len(s) {
		s = s[:k]
	}
	return s
}

// kahanSumInc adds inc to a compensated sum (Neumaier's variant).
func kahanSumInc(inc, sum, c float64) (float64, float64) {
	t := sum + inc
	switch {
	case math.IsInf(t, 0):
		c = 0
	case math.Abs(sum) >= math.Abs(inc):
		c += (sum - t) + inc
	default:
		c += (inc - t) + sum
	}
	return t, c
}

func kahanSum(vals []float64) float64 {
	var sum, c float64
	for _, v := range vals {
		sum, c = kahanSumInc(v, sum, c)
	}
	if math.IsInf(sum, 0) {
		return sum
	}
	return sum + c
}

// mean is the average of vals, using a compensated sum. If the sum overflows
// although every input is finite, it falls back to an incremental mean, which
// divides before adding and so cannot overflow.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	sum := kahanSum(vals)
	if !math.IsInf(sum, 0) {
		return sum / float64(len(vals))
	}
	for _, v := range vals {
		if math.IsInf(v, 0) {
			return sum // a genuine infinity (or NaN from +Inf and -Inf)
		}
	}
	var m, c float64
	for i, v := range vals {
		n := float64(i + 1)
		m, c = kahanSumInc(v/n-m/n, m, c)
	}
	return m + c
}

// variance is the population variance (Welford's algorithm).
func variance(vals []float64) float64 {
	var n, m, m2 float64
	for _, v := range vals {
		n++
		delta := v - m
		m += delta / n
		m2 += delta * (v - m)
	}
	if n == 0 {
		return math.NaN()
	}
	return m2 / n
}

// quantile estimates the q-quantile by linear interpolation between the two
// nearest ranks. NaN values sort first, as in Prometheus.
func quantile(q float64, vals []float64) float64 {
	if len(vals) == 0 || math.IsNaN(q) {
		return math.NaN()
	}
	if q < 0 {
		return math.Inf(-1)
	}
	if q > 1 {
		return math.Inf(1)
	}
	s := append([]float64(nil), vals...)
	sort.Slice(s, func(i, j int) bool { return math.IsNaN(s[i]) || s[i] < s[j] })
	n := float64(len(s))
	rank := q * (n - 1)
	lower := math.Max(0, math.Floor(rank))
	upper := math.Min(n-1, lower+1)
	w := rank - math.Floor(rank)
	return s[int(lower)]*(1-w) + s[int(upper)]*w
}
