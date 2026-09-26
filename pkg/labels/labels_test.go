package labels

import (
	"errors"
	"strings"
	"testing"
)

func TestSetAndWithout(t *testing.T) {
	ls := FromStrings("__name__", "up", "job", "api", "zone", "a")
	if got := ls.Set("instance", "x").String(); got != `up{instance="x", job="api", zone="a"}` {
		t.Errorf("Set insert: %s", got)
	}
	if got := ls.Set("job", "").String(); got != `up{zone="a"}` {
		t.Errorf("Set delete: %s", got)
	}
	if got := ls.Set("aaa", "1")[0].Name; got != "__name__" {
		t.Errorf("Set keeps order: first label %q", got)
	}
	if got := ls.Set("zz", "1")[3].Name; got != "zz" {
		t.Errorf("Set append: %q", got)
	}
	if got := ls.Without("job").DropMetricName().String(); got != `{zone="a"}` {
		t.Errorf("Without: %s", got)
	}
	if got := ls.Keep("zone", "job").String(); got != `{job="api", zone="a"}` {
		t.Errorf("Keep: %s", got)
	}
}

func TestHashAndCompare(t *testing.T) {
	a := FromStrings("__name__", "m", "a", "1")
	b := FromMap(map[string]string{"a": "1", "__name__": "m"})
	if !Equal(a, b) || a.Hash() != b.Hash() || Compare(a, b) != 0 {
		t.Fatal("equal label sets differ")
	}
	// The separator byte keeps "a"+"bc" distinct from "ab"+"c".
	if FromStrings("a", "bc").Hash() == FromStrings("ab", "c").Hash() {
		t.Fatal("hash ignores name/value boundaries")
	}
	if Compare(FromStrings("a", "1"), FromStrings("a", "2")) >= 0 {
		t.Fatal("compare order")
	}
	if FromMap(map[string]string{"a": ""}).Has("a") {
		t.Fatal("empty value should be dropped")
	}
}

func TestMatcher(t *testing.T) {
	cases := []struct {
		t        MatchType
		pattern  string
		value    string
		expected bool
	}{
		{MatchEqual, "a", "a", true},
		{MatchNotEqual, "a", "a", false},
		{MatchRegexp, "a.*", "abc", true},
		{MatchRegexp, "b", "abc", false}, // anchored
		{MatchRegexp, "a|", "", true},
		{MatchNotRegexp, "5..", "500", false},
		{MatchNotRegexp, "5..", "404", true},
		{MatchRegexp, "a.c", "a\nc", true}, // dot matches newline, as in Prometheus
	}
	for _, c := range cases {
		m := MustNewMatcher(c.t, "x", c.pattern)
		if got := m.Matches(c.value); got != c.expected {
			t.Errorf("%s matches %q = %v", m, c.value, got)
		}
	}
	if _, err := NewMatcher(MatchRegexp, "x", "("); err == nil {
		t.Error("invalid regexp accepted")
	}
}

func TestValidate(t *testing.T) {
	ok := FromStrings("__name__", "http_requests_total", "code", "200")
	if err := Validate(ok); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}
	bad := map[string]Labels{
		"no name":        FromStrings("code", "200"),
		"bad name":       FromStrings("__name__", "1abc"),
		"bad label":      FromStrings("__name__", "m", "a-b", "1"),
		"reserved":       FromStrings("__name__", "m", "__stat__", "1"),
		"empty value":    {{"__name__", "m"}, {"a", ""}},
		"unsorted":       {{"__name__", "m"}, {"b", "1"}, {"a", "1"}},
		"duplicate":      {{"__name__", "m"}, {"a", "1"}, {"a", "2"}},
		"long value":     FromStrings("__name__", "m", "a", strings.Repeat("x", MaxValueLength+1)),
		"invalid utf8":   FromStrings("__name__", "m", "a", "\xff"),
		"colon in label": FromStrings("__name__", "m", "a:b", "1"),
	}
	for name, ls := range bad {
		if err := Validate(ls); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
	if !IsValidMetricName("job:rate5m") || IsValidLabelName("job:x") {
		t.Error("colon rules")
	}
}
