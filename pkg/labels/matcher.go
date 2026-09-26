package labels

import (
	"fmt"
	"regexp"
	"strconv"
)

// MatchType is the kind of comparison a Matcher performs.
type MatchType int

const (
	MatchEqual MatchType = iota
	MatchNotEqual
	MatchRegexp
	MatchNotRegexp
)

func (t MatchType) String() string {
	switch t {
	case MatchEqual:
		return "="
	case MatchNotEqual:
		return "!="
	case MatchRegexp:
		return "=~"
	case MatchNotRegexp:
		return "!~"
	}
	return "?"
}

// Matcher selects series whose label Name compares to Value by Type.
// A missing label matches as the empty string, as in Prometheus.
type Matcher struct {
	Type  MatchType
	Name  string
	Value string

	re *regexp.Regexp
}

// NewMatcher builds a matcher. Regular expressions are fully anchored.
func NewMatcher(t MatchType, name, value string) (*Matcher, error) {
	m := &Matcher{Type: t, Name: name, Value: value}
	if t == MatchRegexp || t == MatchNotRegexp {
		re, err := regexp.Compile("^(?s:" + value + ")$")
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression %q: %w", value, err)
		}
		m.re = re
	}
	return m, nil
}

// MustNewMatcher is NewMatcher that panics on error. For tests and constants.
func MustNewMatcher(t MatchType, name, value string) *Matcher {
	m, err := NewMatcher(t, name, value)
	if err != nil {
		panic(err)
	}
	return m
}

// Matches reports whether the label value v satisfies the matcher.
func (m *Matcher) Matches(v string) bool {
	switch m.Type {
	case MatchEqual:
		return v == m.Value
	case MatchNotEqual:
		return v != m.Value
	case MatchRegexp:
		return m.re.MatchString(v)
	case MatchNotRegexp:
		return !m.re.MatchString(v)
	}
	return false
}

// MatchesEmpty reports whether the matcher accepts series lacking the label.
func (m *Matcher) MatchesEmpty() bool { return m.Matches("") }

func (m *Matcher) String() string {
	return m.Name + m.Type.String() + strconv.Quote(m.Value)
}

// MatchLabels reports whether ls satisfies every matcher.
func MatchLabels(ls Labels, ms ...*Matcher) bool {
	for _, m := range ms {
		if !m.Matches(ls.Get(m.Name)) {
			return false
		}
	}
	return true
}
