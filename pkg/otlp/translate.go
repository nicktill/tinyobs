package otlp

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// The translation follows Prometheus's OTLP receiver with its default
// "UnderscoreEscapingWithSuffixes" naming, so metrics sent to TinyObs get the
// same names and labels they would get in Prometheus:
// https://prometheus.io/docs/guides/opentelemetry/

type sample struct {
	lset labels.Labels
	t    int64
	v    float64
}

type translation struct {
	samples  []sample
	metadata map[string]tsdb.Metadata
	rejected int
	errs     []string
}

func (tr *translation) reject(n int, format string, args ...any) {
	tr.rejected += n
	if len(tr.errs) < 5 {
		tr.errs = append(tr.errs, fmt.Sprintf(format, args...))
	}
}

func translate(rms []resourceMetrics, nowMs int64) *translation {
	tr := &translation{metadata: map[string]tsdb.Metadata{}}
	for _, rm := range rms {
		resLabels, info := resourceLabels(rm.attrs)
		latest := int64(0)
		for _, sm := range rm.scopes {
			for _, m := range sm.metrics {
				t := translateMetric(tr, m, resLabels, nowMs)
				if t > latest {
					latest = t
				}
			}
		}
		// Resource attributes other than those mapped to job and instance
		// become labels of a target_info series, as in Prometheus.
		if len(info) > 0 && latest > 0 {
			ls := append(append(labels.Labels{}, resLabels...), info...)
			ls = append(ls, labels.Label{Name: labels.MetricName, Value: "target_info"})
			tr.samples = append(tr.samples, sample{labels.New(ls...), latest, 1})
			tr.metadata["target_info"] = tsdb.Metadata{Type: "gauge", Help: "Target metadata"}
		}
	}
	return tr
}

// resourceLabels maps service.name/namespace to job and service.instance.id
// to instance, and returns the remaining attributes for target_info.
func resourceLabels(attrs []keyValue) (labels.Labels, labels.Labels) {
	var name, ns, inst string
	rest := map[string]string{}
	for _, kv := range attrs {
		switch kv.key {
		case "service.name":
			name = kv.value
		case "service.namespace":
			ns = kv.value
		case "service.instance.id":
			inst = kv.value
		default:
			rest[kv.key] = kv.value
		}
	}
	var out []labels.Label
	if name != "" {
		job := name
		if ns != "" {
			job = ns + "/" + name
		}
		out = append(out, labels.Label{Name: "job", Value: job})
	}
	if inst != "" {
		out = append(out, labels.Label{Name: "instance", Value: inst})
	}
	var info []keyValue
	for k, v := range rest {
		info = append(info, keyValue{k, v})
	}
	return labels.New(out...), normalizeAttrs(info)
}

// normalizeAttrs converts attributes to labels. Names that collide after
// normalization have their values joined with ";", sorted by original key.
func normalizeAttrs(attrs []keyValue) labels.Labels {
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].key < attrs[j].key })
	byName := map[string][]string{}
	var order []string
	for _, kv := range attrs {
		if kv.value == "" {
			continue
		}
		n := normalizeLabelName(kv.key)
		if n == "" {
			continue
		}
		if _, ok := byName[n]; !ok {
			order = append(order, n)
		}
		byName[n] = append(byName[n], kv.value)
	}
	out := make([]labels.Label, 0, len(order))
	for _, n := range order {
		out = append(out, labels.Label{Name: n, Value: strings.Join(byName[n], ";")})
	}
	return labels.New(out...)
}

func normalizeLabelName(s string) string {
	if s == "" {
		return ""
	}
	b := []byte(s)
	for i, c := range b {
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			b[i] = '_'
		}
	}
	s = string(b)
	switch {
	case s[0] >= '0' && s[0] <= '9':
		s = "key_" + s
	case strings.HasPrefix(s, "_"):
		// Leading underscores are reserved in Prometheus ("__" in particular).
		s = "key" + s
	}
	return s
}

var unitNames = map[string]string{
	"d": "days", "h": "hours", "min": "minutes", "s": "seconds", "ms": "milliseconds",
	"us": "microseconds", "ns": "nanoseconds",
	"By": "bytes", "KiBy": "kibibytes", "MiBy": "mebibytes", "GiBy": "gibibytes", "TiBy": "tibibytes",
	"KBy": "kilobytes", "MBy": "megabytes", "GBy": "gigabytes", "TBy": "terabytes",
	"m": "meters", "V": "volts", "A": "amperes", "J": "joules", "W": "watts", "g": "grams",
	"Cel": "celsius", "Hz": "hertz", "%": "percent",
}

var perUnitNames = map[string]string{
	"s": "second", "m": "minute", "h": "hour", "d": "day", "w": "week", "mo": "month", "y": "year",
}

// unitSuffixes returns the name suffixes for a UCUM unit: "By/s" gives
// ("bytes", "second"). Annotations in braces such as "{request}" add nothing.
func unitSuffixes(unit string) (string, string) {
	if strings.ContainsAny(unit, "{}") {
		return "", ""
	}
	main, per, _ := strings.Cut(unit, "/")
	suffix := func(u string, table map[string]string) string {
		u = strings.TrimSpace(u)
		if u == "" || u == "1" {
			return ""
		}
		if n, ok := table[u]; ok {
			return n
		}
		return cleanToken(u)
	}
	return suffix(main, unitNames), suffix(per, perUnitNames)
}

func cleanToken(s string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return '_'
	}, s), "_")
}

// metricName builds the Prometheus name: invalid characters split words,
// unit suffixes are appended, counters end in _total and unit "1" gauges in
// _ratio.
func metricName(name, unit string, counter, gauge bool) string {
	tokens := strings.FieldsFunc(name, func(r rune) bool {
		return !(r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == ':'))
	})
	has := func(t string) bool {
		for _, x := range tokens {
			if x == t {
				return true
			}
		}
		return false
	}
	remove := func(t string) {
		out := tokens[:0]
		for _, x := range tokens {
			if x != t {
				out = append(out, x)
			}
		}
		tokens = out
	}
	mainU, perU := unitSuffixes(unit)
	if mainU != "" && !has(mainU) {
		tokens = append(tokens, mainU)
	}
	if perU != "" && !has(perU) {
		tokens = append(tokens, "per", perU)
	}
	if counter {
		remove("total")
		tokens = append(tokens, "total")
	}
	if unit == "1" && gauge {
		remove("ratio")
		tokens = append(tokens, "ratio")
	}
	n := strings.Join(tokens, "_")
	if n != "" && n[0] >= '0' && n[0] <= '9' {
		n = "_" + n
	}
	return n
}

func timestampMs(nano uint64, nowMs int64) int64 {
	if nano == 0 {
		return nowMs
	}
	return int64(nano / 1e6)
}

func value(v float64, flags uint32) float64 {
	if flags&flagNoRecordedValue != 0 {
		return tsdb.StaleNaN
	}
	return v
}

func formatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// translateMetric appends the samples of one metric and returns its latest timestamp.
func translateMetric(tr *translation, m metric, res labels.Labels, nowMs int64) int64 {
	if m.name == "" {
		tr.reject(m.numPoints, "metric without a name")
		return 0
	}
	var latest int64
	seriesLabels := func(name string, attrs []keyValue, extra ...labels.Label) labels.Labels {
		ls := normalizeAttrs(attrs)
		// Resource labels (job, instance) win over point attributes.
		for _, l := range res {
			ls = ls.Set(l.Name, l.Value)
		}
		for _, l := range extra {
			ls = ls.Set(l.Name, l.Value)
		}
		return ls.Set(labels.MetricName, name)
	}
	add := func(ls labels.Labels, t int64, v float64) {
		tr.samples = append(tr.samples, sample{ls, t, v})
		if t > latest {
			latest = t
		}
	}
	unitName, _ := unitSuffixes(m.unit)

	switch m.kind {
	case kindGauge, kindSum:
		counter := m.kind == kindSum && m.monotonic
		if m.kind == kindSum && m.temporality != temporalityCumulative {
			tr.reject(len(m.numbers), "%s: only cumulative temporality is supported; configure the exporter's temporality preference as cumulative", m.name)
			return 0
		}
		name := metricName(m.name, m.unit, counter, !counter)
		typ := "gauge"
		if counter {
			typ = "counter"
		}
		tr.metadata[name] = tsdb.Metadata{Type: typ, Help: m.description, Unit: unitName}
		for _, p := range m.numbers {
			add(seriesLabels(name, p.attrs), timestampMs(p.timeNano, nowMs), value(p.value, p.flags))
		}
	case kindHistogram:
		if m.temporality != temporalityCumulative {
			tr.reject(len(m.histograms), "%s: only cumulative temporality is supported; configure the exporter's temporality preference as cumulative", m.name)
			return 0
		}
		name := metricName(m.name, m.unit, false, false)
		tr.metadata[name] = tsdb.Metadata{Type: "histogram", Help: m.description, Unit: unitName}
		for _, p := range m.histograms {
			if len(p.buckets) > 0 && len(p.buckets) != len(p.bounds)+1 {
				tr.reject(1, "%s: %d bucket counts for %d bounds", m.name, len(p.buckets), len(p.bounds))
				continue
			}
			t := timestampMs(p.timeNano, nowMs)
			var cum uint64
			for i, bound := range p.bounds {
				if len(p.buckets) > 0 {
					cum += p.buckets[i]
				}
				add(seriesLabels(name+"_bucket", p.attrs, labels.Label{Name: "le", Value: formatFloat(bound)}), t, value(float64(cum), p.flags))
			}
			add(seriesLabels(name+"_bucket", p.attrs, labels.Label{Name: "le", Value: "+Inf"}), t, value(float64(p.count), p.flags))
			add(seriesLabels(name+"_count", p.attrs), t, value(float64(p.count), p.flags))
			if p.hasSum {
				add(seriesLabels(name+"_sum", p.attrs), t, value(p.sum, p.flags))
			}
		}
	case kindSummary:
		name := metricName(m.name, m.unit, false, false)
		tr.metadata[name] = tsdb.Metadata{Type: "summary", Help: m.description, Unit: unitName}
		for _, p := range m.summaries {
			t := timestampMs(p.timeNano, nowMs)
			for _, q := range p.quantiles {
				add(seriesLabels(name, p.attrs, labels.Label{Name: "quantile", Value: formatFloat(q[0])}), t, value(q[1], p.flags))
			}
			add(seriesLabels(name+"_count", p.attrs), t, value(float64(p.count), p.flags))
			add(seriesLabels(name+"_sum", p.attrs), t, value(p.sum, p.flags))
		}
	case kindExpHistogram:
		tr.reject(m.numPoints, "%s: exponential histograms are not supported; use explicit bucket histograms", m.name)
	default:
		tr.reject(m.numPoints, "%s: metric has no data", m.name)
	}
	return latest
}
