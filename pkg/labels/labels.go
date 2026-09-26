// Package labels defines the label set that identifies a time series and the
// matchers used to select series. The model follows Prometheus: a series is a
// set of name/value pairs sorted by name, and the metric name is stored under
// the reserved label "__name__".
package labels

import (
	"sort"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// MetricName is the reserved label holding a series' metric name.
const MetricName = "__name__"

// Label is a single name/value pair.
type Label struct {
	Name, Value string
}

// Labels is a label set sorted by name. Treat it as immutable once built.
type Labels []Label

// New returns a sorted label set built from the given labels.
func New(ls ...Label) Labels {
	out := make(Labels, len(ls))
	copy(out, ls)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FromMap builds a sorted label set from a map. Empty values are dropped,
// since an empty label value is equivalent to the label being absent.
func FromMap(m map[string]string) Labels {
	out := make(Labels, 0, len(m))
	for k, v := range m {
		if v != "" {
			out = append(out, Label{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FromStrings builds a label set from alternating name/value strings.
func FromStrings(ss ...string) Labels {
	if len(ss)%2 != 0 {
		panic("labels.FromStrings: odd number of strings")
	}
	ls := make([]Label, 0, len(ss)/2)
	for i := 0; i < len(ss); i += 2 {
		ls = append(ls, Label{ss[i], ss[i+1]})
	}
	return New(ls...)
}

// Get returns the value of the named label, or "" if absent.
func (ls Labels) Get(name string) string {
	for _, l := range ls {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// Has reports whether the named label is present.
func (ls Labels) Has(name string) bool {
	for _, l := range ls {
		if l.Name == name {
			return true
		}
	}
	return false
}

// Map returns the label set as a map.
func (ls Labels) Map() map[string]string {
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}

// Hash returns a hash of the label set. Equal label sets hash equally.
func (ls Labels) Hash() uint64 {
	b := make([]byte, 0, 256)
	for _, l := range ls {
		b = append(b, l.Name...)
		b = append(b, 0xff)
		b = append(b, l.Value...)
		b = append(b, 0xff)
	}
	return xxhash.Sum64(b)
}

// Equal reports whether two label sets are identical.
func Equal(a, b Labels) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Compare orders label sets lexicographically, name before value.
func Compare(a, b Labels) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := strings.Compare(a[i].Name, b[i].Name); c != 0 {
			return c
		}
		if c := strings.Compare(a[i].Value, b[i].Value); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

// Without returns a copy of ls with the named labels removed.
func (ls Labels) Without(names ...string) Labels {
	out := make(Labels, 0, len(ls))
Outer:
	for _, l := range ls {
		for _, n := range names {
			if l.Name == n {
				continue Outer
			}
		}
		out = append(out, l)
	}
	return out
}

// Keep returns a copy of ls containing only the named labels.
func (ls Labels) Keep(names ...string) Labels {
	out := make(Labels, 0, len(names))
	for _, l := range ls {
		for _, n := range names {
			if l.Name == n {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// DropMetricName returns ls without the __name__ label.
func (ls Labels) DropMetricName() Labels {
	if !ls.Has(MetricName) {
		return ls
	}
	return ls.Without(MetricName)
}

// Set returns a copy of ls with name set to value. An empty value removes it.
func (ls Labels) Set(name, value string) Labels {
	out := make(Labels, 0, len(ls)+1)
	done := false
	for _, l := range ls {
		if l.Name == name {
			if value != "" {
				out = append(out, Label{name, value})
			}
			done = true
			continue
		}
		if !done && l.Name > name {
			if value != "" {
				out = append(out, Label{name, value})
			}
			done = true
		}
		out = append(out, l)
	}
	if !done && value != "" {
		out = append(out, Label{name, value})
	}
	return out
}

// String formats ls in PromQL selector syntax, e.g. up{job="api"}.
func (ls Labels) String() string {
	var b strings.Builder
	name := ls.Get(MetricName)
	b.WriteString(name)
	b.WriteByte('{')
	first := true
	for _, l := range ls {
		if l.Name == MetricName {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l.Value))
	}
	b.WriteByte('}')
	return b.String()
}
