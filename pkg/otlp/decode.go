// Package otlp receives OpenTelemetry metrics over OTLP/HTTP (protobuf or
// JSON) and stores them the way Prometheus's OTLP receiver would.
package otlp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"google.golang.org/protobuf/encoding/protowire"
)

// The decoded subset of opentelemetry/proto/metrics/v1/metrics.proto.

type resourceMetrics struct {
	attrs  []keyValue
	scopes []scopeMetrics
}

type scopeMetrics struct {
	metrics []metric
}

type metricKind int

const (
	kindUnknown metricKind = iota
	kindGauge
	kindSum
	kindHistogram
	kindExpHistogram
	kindSummary
)

const (
	temporalityUnspecified = 0
	temporalityDelta       = 1
	temporalityCumulative  = 2
)

// flagNoRecordedValue marks a point that ends a series (Prometheus staleness).
const flagNoRecordedValue = 1

type metric struct {
	name, description, unit string
	kind                    metricKind
	monotonic               bool
	temporality             int
	numbers                 []numberPoint
	histograms              []histogramPoint
	summaries               []summaryPoint
	numPoints               int // including kinds we don't decode (exponential histograms)
}

type keyValue struct {
	key   string
	value string // stringified AnyValue
}

type numberPoint struct {
	attrs    []keyValue
	timeNano uint64
	value    float64
	flags    uint32
}

type histogramPoint struct {
	attrs    []keyValue
	timeNano uint64
	count    uint64
	sum      float64
	hasSum   bool
	buckets  []uint64
	bounds   []float64
	flags    uint32
}

type summaryPoint struct {
	attrs     []keyValue
	timeNano  uint64
	count     uint64
	sum       float64
	quantiles [][2]float64 // quantile, value
	flags     uint32
}

// Protobuf decoding.

var errTruncated = errors.New("truncated protobuf message")

// fields calls fn for each field in b. fn returns the number of bytes it
// consumed from v, or -1 to skip the field.
func fields(b []byte, fn func(num protowire.Number, typ protowire.Type, v []byte) (int, error)) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return errTruncated
		}
		b = b[n:]
		m, err := fn(num, typ, b)
		if err != nil {
			return err
		}
		if m < 0 {
			m = protowire.ConsumeFieldValue(num, typ, b)
		}
		if m < 0 {
			return errTruncated
		}
		b = b[m:]
	}
	return nil
}

func bytesField(typ protowire.Type, b []byte) ([]byte, int, error) {
	if typ != protowire.BytesType {
		return nil, 0, fmt.Errorf("unexpected wire type %d", typ)
	}
	v, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return nil, 0, errTruncated
	}
	return v, n, nil
}

func fixed64Field(typ protowire.Type, b []byte) (uint64, int, error) {
	if typ != protowire.Fixed64Type {
		return 0, 0, fmt.Errorf("unexpected wire type %d", typ)
	}
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, 0, errTruncated
	}
	return v, n, nil
}

func varintField(typ protowire.Type, b []byte) (uint64, int, error) {
	if typ != protowire.VarintType {
		return 0, 0, fmt.Errorf("unexpected wire type %d", typ)
	}
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, 0, errTruncated
	}
	return v, n, nil
}

// decodeProto decodes an ExportMetricsServiceRequest.
func decodeProto(b []byte) ([]resourceMetrics, error) {
	var out []resourceMetrics
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		if num != 1 {
			return -1, nil
		}
		msg, n, err := bytesField(typ, v)
		if err != nil {
			return 0, err
		}
		rm, err := decodeResourceMetrics(msg)
		if err != nil {
			return 0, err
		}
		out = append(out, rm)
		return n, nil
	})
	return out, err
}

func decodeResourceMetrics(b []byte) (resourceMetrics, error) {
	var rm resourceMetrics
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 1: // Resource
			msg, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			return n, fields(msg, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
				if num != 1 {
					return -1, nil
				}
				kv, n, err := decodeKeyValueField(typ, v)
				if err == nil {
					rm.attrs = append(rm.attrs, kv)
				}
				return n, err
			})
		case 2: // ScopeMetrics
			msg, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			sm, err := decodeScopeMetrics(msg)
			if err != nil {
				return 0, err
			}
			rm.scopes = append(rm.scopes, sm)
			return n, nil
		}
		return -1, nil
	})
	return rm, err
}

func decodeScopeMetrics(b []byte) (scopeMetrics, error) {
	var sm scopeMetrics
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		if num != 2 {
			return -1, nil
		}
		msg, n, err := bytesField(typ, v)
		if err != nil {
			return 0, err
		}
		m, err := decodeMetric(msg)
		if err != nil {
			return 0, err
		}
		sm.metrics = append(sm.metrics, m)
		return n, nil
	})
	return sm, err
}

func decodeMetric(b []byte) (metric, error) {
	var m metric
	str := func(typ protowire.Type, v []byte, dst *string) (int, error) {
		s, n, err := bytesField(typ, v)
		*dst = string(s)
		return n, err
	}
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 1:
			return str(typ, v, &m.name)
		case 2:
			return str(typ, v, &m.description)
		case 3:
			return str(typ, v, &m.unit)
		case 5, 7, 9, 10, 11:
			msg, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			switch num {
			case 5:
				m.kind = kindGauge
			case 7:
				m.kind = kindSum
			case 9:
				m.kind = kindHistogram
			case 10:
				m.kind = kindExpHistogram
			case 11:
				m.kind = kindSummary
			}
			return n, decodeData(msg, &m)
		}
		return -1, nil
	})
	return m, err
}

// decodeData decodes Gauge, Sum, Histogram, ExponentialHistogram or Summary.
func decodeData(b []byte, m *metric) error {
	return fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch {
		case num == 1: // data_points
			msg, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			m.numPoints++
			switch m.kind {
			case kindGauge, kindSum:
				p, err := decodeNumberPoint(msg)
				m.numbers = append(m.numbers, p)
				return n, err
			case kindHistogram:
				p, err := decodeHistogramPoint(msg)
				m.histograms = append(m.histograms, p)
				return n, err
			case kindSummary:
				p, err := decodeSummaryPoint(msg)
				m.summaries = append(m.summaries, p)
				return n, err
			}
			return n, nil // exponential histogram points are counted, not decoded
		case num == 2 && (m.kind == kindSum || m.kind == kindHistogram || m.kind == kindExpHistogram):
			t, n, err := varintField(typ, v)
			m.temporality = int(t)
			return n, err
		case num == 3 && m.kind == kindSum:
			t, n, err := varintField(typ, v)
			m.monotonic = t != 0
			return n, err
		}
		return -1, nil
	})
}

func decodeKeyValueField(typ protowire.Type, v []byte) (keyValue, int, error) {
	msg, n, err := bytesField(typ, v)
	if err != nil {
		return keyValue{}, 0, err
	}
	var kv keyValue
	err = fields(msg, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 1:
			s, n, err := bytesField(typ, v)
			kv.key = string(s)
			return n, err
		case 2:
			s, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			kv.value, err = decodeAnyValue(s)
			return n, err
		}
		return -1, nil
	})
	return kv, n, err
}

// decodeAnyValue renders an AnyValue as a label value string.
func decodeAnyValue(b []byte) (string, error) {
	var out any
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 1:
			s, n, err := bytesField(typ, v)
			out = string(s)
			return n, err
		case 2:
			x, n, err := varintField(typ, v)
			out = x != 0
			return n, err
		case 3:
			x, n, err := varintField(typ, v)
			out = int64(x)
			return n, err
		case 4:
			x, n, err := fixed64Field(typ, v)
			out = math.Float64frombits(x)
			return n, err
		case 5, 6: // ArrayValue / KeyValueList: rendered as JSON
			msg, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			if num == 5 {
				var arr []any
				err = fields(msg, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
					if num != 1 {
						return -1, nil
					}
					s, n, err := bytesField(typ, v)
					if err != nil {
						return 0, err
					}
					str, err := decodeAnyValue(s)
					arr = append(arr, str)
					return n, err
				})
				out = arr
			} else {
				obj := map[string]string{}
				err = fields(msg, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
					if num != 1 {
						return -1, nil
					}
					kv, n, err := decodeKeyValueField(typ, v)
					obj[kv.key] = kv.value
					return n, err
				})
				out = obj
			}
			return n, err
		case 7:
			s, n, err := bytesField(typ, v)
			out = base64.StdEncoding.EncodeToString(s)
			return n, err
		}
		return -1, nil
	})
	return stringify(out), err
}

func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func decodeNumberPoint(b []byte) (numberPoint, error) {
	var p numberPoint
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 7:
			kv, n, err := decodeKeyValueField(typ, v)
			p.attrs = append(p.attrs, kv)
			return n, err
		case 3:
			t, n, err := fixed64Field(typ, v)
			p.timeNano = t
			return n, err
		case 4:
			x, n, err := fixed64Field(typ, v)
			p.value = math.Float64frombits(x)
			return n, err
		case 6:
			x, n, err := fixed64Field(typ, v)
			p.value = float64(int64(x))
			return n, err
		case 8:
			f, n, err := varintField(typ, v)
			p.flags = uint32(f)
			return n, err
		}
		return -1, nil
	})
	return p, err
}

// packedOrSingle reads a repeated fixed64 field that may be packed or not.
func packedOrSingle(typ protowire.Type, v []byte, add func(uint64)) (int, error) {
	if typ == protowire.Fixed64Type {
		x, n, err := fixed64Field(typ, v)
		if err == nil {
			add(x)
		}
		return n, err
	}
	msg, n, err := bytesField(typ, v)
	if err != nil {
		return 0, err
	}
	for len(msg) > 0 {
		x, m := protowire.ConsumeFixed64(msg)
		if m < 0 {
			return 0, errTruncated
		}
		add(x)
		msg = msg[m:]
	}
	return n, nil
}

func decodeHistogramPoint(b []byte) (histogramPoint, error) {
	var p histogramPoint
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 9:
			kv, n, err := decodeKeyValueField(typ, v)
			p.attrs = append(p.attrs, kv)
			return n, err
		case 3:
			t, n, err := fixed64Field(typ, v)
			p.timeNano = t
			return n, err
		case 4:
			c, n, err := fixed64Field(typ, v)
			p.count = c
			return n, err
		case 5:
			x, n, err := fixed64Field(typ, v)
			p.sum, p.hasSum = math.Float64frombits(x), true
			return n, err
		case 6:
			return packedOrSingle(typ, v, func(x uint64) { p.buckets = append(p.buckets, x) })
		case 7:
			return packedOrSingle(typ, v, func(x uint64) { p.bounds = append(p.bounds, math.Float64frombits(x)) })
		case 10:
			f, n, err := varintField(typ, v)
			p.flags = uint32(f)
			return n, err
		}
		return -1, nil
	})
	return p, err
}

func decodeSummaryPoint(b []byte) (summaryPoint, error) {
	var p summaryPoint
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
		switch num {
		case 7:
			kv, n, err := decodeKeyValueField(typ, v)
			p.attrs = append(p.attrs, kv)
			return n, err
		case 3:
			t, n, err := fixed64Field(typ, v)
			p.timeNano = t
			return n, err
		case 4:
			c, n, err := fixed64Field(typ, v)
			p.count = c
			return n, err
		case 5:
			x, n, err := fixed64Field(typ, v)
			p.sum = math.Float64frombits(x)
			return n, err
		case 6:
			msg, n, err := bytesField(typ, v)
			if err != nil {
				return 0, err
			}
			var q [2]float64
			err = fields(msg, func(num protowire.Number, typ protowire.Type, v []byte) (int, error) {
				if num != 1 && num != 2 {
					return -1, nil
				}
				x, n, err := fixed64Field(typ, v)
				q[num-1] = math.Float64frombits(x)
				return n, err
			})
			p.quantiles = append(p.quantiles, q)
			return n, err
		case 8:
			f, n, err := varintField(typ, v)
			p.flags = uint32(f)
			return n, err
		}
		return -1, nil
	})
	return p, err
}

// OTLP/JSON decoding. The JSON mapping uses lowerCamelCase names, encodes
// 64-bit integers as strings, and may encode special floats as strings.

type jsonRequest struct {
	ResourceMetrics []struct {
		Resource struct {
			Attributes []jsonKeyValue `json:"attributes"`
		} `json:"resource"`
		ScopeMetrics []struct {
			Metrics []jsonMetric `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type jsonKeyValue struct {
	Key   string    `json:"key"`
	Value jsonValue `json:"value"`
}

type jsonValue struct {
	StringValue *string    `json:"stringValue"`
	BoolValue   *bool      `json:"boolValue"`
	IntValue    *jsonInt   `json:"intValue"`
	DoubleValue *jsonFloat `json:"doubleValue"`
	ArrayValue  *struct {
		Values []jsonValue `json:"values"`
	} `json:"arrayValue"`
	KvlistValue *struct {
		Values []jsonKeyValue `json:"values"`
	} `json:"kvlistValue"`
	BytesValue *string `json:"bytesValue"`
}

func (v jsonValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	case v.IntValue != nil:
		return strconv.FormatInt(int64(*v.IntValue), 10)
	case v.DoubleValue != nil:
		return strconv.FormatFloat(float64(*v.DoubleValue), 'f', -1, 64)
	case v.ArrayValue != nil:
		arr := make([]any, len(v.ArrayValue.Values))
		for i, x := range v.ArrayValue.Values {
			arr[i] = x.String()
		}
		return stringify(arr)
	case v.KvlistValue != nil:
		obj := map[string]string{}
		for _, kv := range v.KvlistValue.Values {
			obj[kv.Key] = kv.Value.String()
		}
		return stringify(obj)
	case v.BytesValue != nil:
		return *v.BytesValue
	}
	return ""
}

// jsonInt accepts a JSON number or a decimal string.
type jsonInt int64

func (i *jsonInt) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		u, uerr := strconv.ParseUint(s, 10, 64)
		if uerr != nil {
			return fmt.Errorf("invalid integer %s", b)
		}
		v = int64(u)
	}
	*i = jsonInt(v)
	return nil
}

// jsonFloat accepts a JSON number or "NaN", "Infinity", "-Infinity".
type jsonFloat float64

func (f *jsonFloat) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' {
		s = s[1 : len(s)-1]
	}
	switch s {
	case "NaN":
		*f = jsonFloat(math.NaN())
		return nil
	case "Infinity":
		*f = jsonFloat(math.Inf(1))
		return nil
	case "-Infinity":
		*f = jsonFloat(math.Inf(-1))
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid number %s", b)
	}
	*f = jsonFloat(v)
	return nil
}

type jsonDataPoint struct {
	Attributes     []jsonKeyValue `json:"attributes"`
	TimeUnixNano   jsonInt        `json:"timeUnixNano"`
	AsDouble       *jsonFloat     `json:"asDouble"`
	AsInt          *jsonInt       `json:"asInt"`
	Count          jsonInt        `json:"count"`
	Sum            *jsonFloat     `json:"sum"`
	BucketCounts   []jsonInt      `json:"bucketCounts"`
	ExplicitBounds []jsonFloat    `json:"explicitBounds"`
	QuantileValues []struct {
		Quantile jsonFloat `json:"quantile"`
		Value    jsonFloat `json:"value"`
	} `json:"quantileValues"`
	Flags uint32 `json:"flags"`
}

type jsonData struct {
	DataPoints             []jsonDataPoint `json:"dataPoints"`
	AggregationTemporality json.RawMessage `json:"aggregationTemporality"`
	IsMonotonic            bool            `json:"isMonotonic"`
}

type jsonMetric struct {
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	Unit                 string    `json:"unit"`
	Gauge                *jsonData `json:"gauge"`
	Sum                  *jsonData `json:"sum"`
	Histogram            *jsonData `json:"histogram"`
	ExponentialHistogram *jsonData `json:"exponentialHistogram"`
	Summary              *jsonData `json:"summary"`
}

func parseTemporality(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return temporalityUnspecified, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("invalid aggregationTemporality %s", raw)
	}
	switch s {
	case "AGGREGATION_TEMPORALITY_DELTA":
		return temporalityDelta, nil
	case "AGGREGATION_TEMPORALITY_CUMULATIVE":
		return temporalityCumulative, nil
	}
	return temporalityUnspecified, nil
}

func jsonAttrs(kvs []jsonKeyValue) []keyValue {
	out := make([]keyValue, len(kvs))
	for i, kv := range kvs {
		out[i] = keyValue{kv.Key, kv.Value.String()}
	}
	return out
}

func decodeJSON(b []byte) ([]resourceMetrics, error) {
	var req jsonRequest
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, err
	}
	var out []resourceMetrics
	for _, jrm := range req.ResourceMetrics {
		rm := resourceMetrics{attrs: jsonAttrs(jrm.Resource.Attributes)}
		for _, jsm := range jrm.ScopeMetrics {
			var sm scopeMetrics
			for _, jm := range jsm.Metrics {
				m := metric{name: jm.Name, description: jm.Description, unit: jm.Unit}
				var d *jsonData
				switch {
				case jm.Gauge != nil:
					m.kind, d = kindGauge, jm.Gauge
				case jm.Sum != nil:
					m.kind, d = kindSum, jm.Sum
					m.monotonic = jm.Sum.IsMonotonic
				case jm.Histogram != nil:
					m.kind, d = kindHistogram, jm.Histogram
				case jm.ExponentialHistogram != nil:
					m.kind, d = kindExpHistogram, jm.ExponentialHistogram
				case jm.Summary != nil:
					m.kind, d = kindSummary, jm.Summary
				}
				if d != nil {
					t, err := parseTemporality(d.AggregationTemporality)
					if err != nil {
						return nil, err
					}
					m.temporality = t
					m.numPoints = len(d.DataPoints)
					for _, p := range d.DataPoints {
						attrs := jsonAttrs(p.Attributes)
						switch m.kind {
						case kindGauge, kindSum:
							np := numberPoint{attrs: attrs, timeNano: uint64(p.TimeUnixNano), flags: p.Flags}
							if p.AsDouble != nil {
								np.value = float64(*p.AsDouble)
							} else if p.AsInt != nil {
								np.value = float64(*p.AsInt)
							}
							m.numbers = append(m.numbers, np)
						case kindHistogram:
							hp := histogramPoint{attrs: attrs, timeNano: uint64(p.TimeUnixNano), count: uint64(p.Count), flags: p.Flags}
							if p.Sum != nil {
								hp.sum, hp.hasSum = float64(*p.Sum), true
							}
							for _, c := range p.BucketCounts {
								hp.buckets = append(hp.buckets, uint64(c))
							}
							for _, b := range p.ExplicitBounds {
								hp.bounds = append(hp.bounds, float64(b))
							}
							m.histograms = append(m.histograms, hp)
						case kindSummary:
							spt := summaryPoint{attrs: attrs, timeNano: uint64(p.TimeUnixNano), count: uint64(p.Count), flags: p.Flags}
							if p.Sum != nil {
								spt.sum = float64(*p.Sum)
							}
							for _, q := range p.QuantileValues {
								spt.quantiles = append(spt.quantiles, [2]float64{float64(q.Quantile), float64(q.Value)})
							}
							m.summaries = append(m.summaries, spt)
						}
					}
				}
				sm.metrics = append(sm.metrics, m)
			}
			rm.scopes = append(rm.scopes, sm)
		}
		out = append(out, rm)
	}
	return out, nil
}
