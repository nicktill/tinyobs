// Package scrape pulls metrics from Prometheus-style /metrics endpoints.
package scrape

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nicktill/tinyobs/pkg/labels"
)

// Sample is one parsed sample. T is 0 when the exposition carried no timestamp.
type Sample struct {
	Labels labels.Labels
	T      int64
	V      float64
}

// Family is the metadata of a metric family.
type Family struct {
	Name string
	Type string // counter, gauge, histogram, summary, unknown, ...
	Help string
	Unit string
}

// ParseResult is a parsed exposition.
type ParseResult struct {
	Samples  []Sample
	Families map[string]*Family
}

// Parse reads the Prometheus text format (0.0.4) or OpenMetrics text format.
// OpenMetrics exemplars are ignored, as are "_created" series, which carry a
// creation time rather than a value.
func Parse(data []byte, openMetrics bool) (*ParseResult, error) {
	res := &ParseResult{Families: map[string]*Family{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	createdFor := map[string]bool{} // families whose _created series are skipped
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if strings.TrimSpace(text) == "" {
			if openMetrics {
				return nil, fmt.Errorf("line %d: empty lines are not allowed in OpenMetrics", line)
			}
			continue
		}
		if text[0] == '#' {
			if err := parseComment(text, res, createdFor); err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			if openMetrics && text == "# EOF" {
				return res, nil
			}
			continue
		}
		s, name, err := parseSample(text, openMetrics)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if strings.HasSuffix(name, "_created") && createdFor[strings.TrimSuffix(name, "_created")] {
			continue
		}
		res.Samples = append(res.Samples, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if openMetrics {
		return nil, fmt.Errorf("OpenMetrics exposition is missing \"# EOF\"")
	}
	return res, nil
}

func parseComment(text string, res *ParseResult, createdFor map[string]bool) error {
	fields := strings.SplitN(text, " ", 4)
	if len(fields) < 3 {
		return nil // an ordinary comment
	}
	kind, name := fields[1], fields[2]
	rest := ""
	if len(fields) == 4 {
		rest = fields[3]
	}
	switch kind {
	case "HELP", "TYPE", "UNIT":
	default:
		return nil
	}
	if !labels.IsValidMetricName(name) {
		return fmt.Errorf("invalid metric name %q in %s", name, kind)
	}
	f := res.Families[name]
	if f == nil {
		f = &Family{Name: name, Type: "unknown"}
		res.Families[name] = f
	}
	switch kind {
	case "HELP":
		f.Help = unescapeHelp(rest)
	case "TYPE":
		switch rest {
		case "counter", "gauge", "histogram", "summary", "untyped", "unknown", "gaugehistogram", "stateset", "info":
		default:
			return fmt.Errorf("invalid metric type %q", rest)
		}
		if rest == "untyped" {
			rest = "unknown"
		}
		f.Type = rest
		if rest == "counter" || rest == "histogram" || rest == "summary" || rest == "gaugehistogram" {
			createdFor[name] = true
			createdFor[strings.TrimSuffix(name, "_total")] = true
		}
	case "UNIT":
		f.Unit = rest
	}
	return nil
}

func unescapeHelp(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\\`, `\`, `\n`, "\n").Replace(s)
}

// parseSample parses `name{l="v",...} value [timestamp] [# exemplar]`.
func parseSample(text string, openMetrics bool) (Sample, string, error) {
	i := 0
	for i < len(text) && isNameChar(text[i], i == 0) {
		i++
	}
	name := text[:i]
	if !labels.IsValidMetricName(name) {
		return Sample{}, "", fmt.Errorf("invalid metric name in %q", truncate(text))
	}
	ls := []labels.Label{{Name: labels.MetricName, Value: name}}
	if i < len(text) && text[i] == '{' {
		var err error
		var n int
		ls, n, err = parseLabels(text[i:], ls)
		if err != nil {
			return Sample{}, "", err
		}
		i += n
	}
	rest := strings.TrimLeft(text[i:], " \t")
	if openMetrics {
		if j := strings.Index(rest, " # "); j >= 0 {
			rest = rest[:j] // exemplar
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || len(fields) > 2 {
		return Sample{}, "", fmt.Errorf("expected value and optional timestamp in %q", truncate(text))
	}
	v, err := parseValue(fields[0])
	if err != nil {
		return Sample{}, "", err
	}
	var ts int64
	if len(fields) == 2 {
		if openMetrics {
			// OpenMetrics timestamps are seconds, possibly fractional.
			f, err := strconv.ParseFloat(fields[1], 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return Sample{}, "", fmt.Errorf("invalid timestamp %q", fields[1])
			}
			ts = int64(math.Round(f * 1000))
		} else {
			ts, err = strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return Sample{}, "", fmt.Errorf("invalid timestamp %q", fields[1])
			}
		}
	}
	lset := labels.New(ls...)
	for k := 1; k < len(lset); k++ {
		if lset[k].Name == lset[k-1].Name {
			return Sample{}, "", fmt.Errorf("duplicate label %q in %q", lset[k].Name, truncate(text))
		}
	}
	// Empty label values mean "absent".
	out := lset[:0]
	for _, l := range lset {
		if l.Value != "" {
			out = append(out, l)
		}
	}
	return Sample{Labels: out, T: ts, V: v}, name, nil
}

func isNameChar(c byte, first bool) bool {
	return c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (!first && c >= '0' && c <= '9')
}

func parseLabels(s string, ls []labels.Label) ([]labels.Label, int, error) {
	i := 1 // after '{'
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] == '}' {
			return ls, i + 1, nil
		}
		start := i
		for i < len(s) && isNameChar(s[i], i == start) && s[i] != ':' {
			i++
		}
		name := s[start:i]
		if name == "" || i >= len(s) || s[i] != '=' {
			return nil, 0, fmt.Errorf("invalid label in %q", truncate(s))
		}
		i++
		if i >= len(s) || s[i] != '"' {
			return nil, 0, fmt.Errorf("label value must be quoted in %q", truncate(s))
		}
		i++
		var b strings.Builder
		for {
			if i >= len(s) {
				return nil, 0, fmt.Errorf("unterminated label value in %q", truncate(s))
			}
			c := s[i]
			if c == '"' {
				i++
				break
			}
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case '\\':
					b.WriteByte('\\')
				case '"':
					b.WriteByte('"')
				case 'n':
					b.WriteByte('\n')
				default:
					return nil, 0, fmt.Errorf("invalid escape in label value in %q", truncate(s))
				}
				i += 2
				continue
			}
			b.WriteByte(c)
			i++
		}
		if name == labels.MetricName {
			return nil, 0, fmt.Errorf("label %q not allowed in %q", name, truncate(s))
		}
		ls = append(ls, labels.Label{Name: name, Value: b.String()})
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		if i < len(s) && s[i] == '}' {
			return ls, i + 1, nil
		}
		return nil, 0, fmt.Errorf("expected \",\" or \"}\" in %q", truncate(s))
	}
}

func parseValue(s string) (float64, error) {
	switch s {
	case "NaN":
		return math.NaN(), nil
	case "+Inf", "Inf":
		return math.Inf(1), nil
	case "-Inf":
		return math.Inf(-1), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	return v, nil
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
