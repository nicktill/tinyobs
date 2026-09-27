package metrics

import (
	"strings"
	"time"
)

// seriesSet holds the current value of each label combination of one metric.
// Instruments update it in memory; the client reads a snapshot of every
// series once per flush, the way Prometheus client libraries expose state to
// a scrape. Sending a sample per update instead would make traffic grow with
// request rate rather than with the number of series.
type seriesSet[V any] struct {
	m map[string]*entry[V]
}

type entry[V any] struct {
	labels map[string]string
	v      V
}

func newSeriesSet[V any]() seriesSet[V] { return seriesSet[V]{m: map[string]*entry[V]{}} }

// get returns the entry for the label pairs, creating it with init if needed.
// Caller holds the instrument's lock.
func (s seriesSet[V]) get(pairs []string, init func() V) *entry[V] {
	k := key(pairs)
	e := s.m[k]
	if e == nil {
		e = &entry[V]{labels: labelMap(pairs), v: init()}
		s.m[k] = e
	}
	return e
}

// key joins label pairs with a byte that cannot appear in valid UTF-8, so
// label values containing commas or other separators cannot collide.
func key(pairs []string) string { return strings.Join(pairs, "\xff") }

// labelMap turns alternating name/value pairs into a map. A trailing name
// without a value is ignored.
func labelMap(pairs []string) map[string]string {
	m := make(map[string]string, len(pairs)/2+1)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func withLabel(m map[string]string, name, value string) map[string]string {
	out := copyLabels(m)
	out[name] = value
	return out
}

// Collector is implemented by every instrument: it returns the current value
// of each series, stamped with ts.
type Collector interface {
	Collect(ts time.Time) []Metric
}
