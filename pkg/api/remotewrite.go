package api

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/klauspost/compress/snappy"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// maxWriteBody bounds a compressed remote write request. Prometheus sends
// at most a few MB per request (max_samples_per_send defaults to 2000).
const maxWriteBody = 32 << 20

// remoteWrite accepts Prometheus remote write 1.0 requests: a snappy-compressed
// prometheus.WriteRequest protobuf. Prometheus, Grafana Alloy, the OTel
// Collector and vmagent can all push to it.
//
// Status codes follow the spec: 400 tells the sender not to retry (bad or
// rejected data), 5xx tells it to retry.
func (a *API) remoteWrite(w http.ResponseWriter, r *http.Request) {
	if v := r.Header.Get("Content-Type"); v != "" && !strings.HasPrefix(v, "application/x-protobuf") {
		http.Error(w, "unsupported content type "+v, http.StatusUnsupportedMediaType)
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "io.prometheus.write.v2") {
		http.Error(w, "remote write 2.0 is not supported; configure protobuf_message: prometheus.WriteRequest", http.StatusUnsupportedMediaType)
		return
	}
	compressed, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWriteBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	buf, err := snappy.Decode(nil, compressed)
	if err != nil {
		http.Error(w, "snappy: "+err.Error(), http.StatusBadRequest)
		return
	}
	app := a.DB.Appender()
	if err := decodeWriteRequest(buf, app.Append, a.setMetadata); err != nil {
		http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := app.Commit()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Out-of-order and over-limit samples will never be accepted, so a retry
	// would only repeat the failure: report them as a 400, like Prometheus.
	if res.NumRejected() > 0 {
		http.Error(w, fmt.Sprintf("%d samples rejected: %v", res.NumRejected(), res.FirstError), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setMetadata(family string, md tsdb.Metadata) {
	a.DB.SetMetadata(family, md) // best effort; metadata is advisory
}

var errMalformed = errors.New("malformed protobuf")

// decodeWriteRequest walks a prometheus.WriteRequest:
//
//	WriteRequest   { repeated TimeSeries timeseries = 1; repeated MetricMetadata metadata = 3; }
//	TimeSeries     { repeated Label labels = 1; repeated Sample samples = 2; }
//	Label          { string name = 1; string value = 2; }
//	Sample         { double value = 1; int64 timestamp = 2; }
//	MetricMetadata { MetricType type = 1; string metric_family_name = 2; string help = 4; string unit = 5; }
//
// Hand-decoding the five messages avoids generated code and a gogo/protobuf
// dependency. Unknown fields (exemplars, histograms) are skipped.
func decodeWriteRequest(b []byte, add func(labels.Labels, int64, float64), meta func(string, tsdb.Metadata)) error {
	return fields(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			return decodeTimeSeries(v, add)
		case num == 3 && typ == protowire.BytesType:
			return decodeMetadata(v, meta)
		}
		return nil
	})
}

func decodeTimeSeries(b []byte, add func(labels.Labels, int64, float64)) error {
	lm := map[string]string{}
	type sample struct {
		t int64
		v float64
	}
	var samples []sample
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 1:
			var name, value string
			if err := fields(v, func(n protowire.Number, t protowire.Type, v []byte) error {
				if t == protowire.BytesType && n == 1 {
					name = string(v)
				} else if t == protowire.BytesType && n == 2 {
					value = string(v)
				}
				return nil
			}); err != nil {
				return err
			}
			if value != "" {
				lm[name] = value
			}
		case 2:
			var s sample
			if err := fields(v, func(n protowire.Number, t protowire.Type, v []byte) error {
				if n == 1 && t == protowire.Fixed64Type {
					s.v = math.Float64frombits(fixed64(v))
				} else if n == 2 && t == protowire.VarintType {
					s.t = int64(varint(v))
				}
				return nil
			}); err != nil {
				return err
			}
			samples = append(samples, s)
		}
		return nil
	})
	if err != nil {
		return err
	}
	lset := labels.FromMap(lm)
	for _, s := range samples {
		add(lset, s.t, s.v)
	}
	return nil
}

var metricTypes = map[uint64]string{0: "unknown", 1: "counter", 2: "gauge", 3: "histogram", 4: "gaugehistogram", 5: "summary", 6: "info", 7: "stateset"}

func decodeMetadata(b []byte, meta func(string, tsdb.Metadata)) error {
	var family string
	md := tsdb.Metadata{Type: "unknown"}
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == 1 && typ == protowire.VarintType:
			if t, ok := metricTypes[varint(v)]; ok {
				md.Type = t
			}
		case num == 2 && typ == protowire.BytesType:
			family = string(v)
		case num == 4 && typ == protowire.BytesType:
			md.Help = string(v)
		case num == 5 && typ == protowire.BytesType:
			md.Unit = string(v)
		}
		return nil
	})
	if err == nil && family != "" {
		meta(family, md)
	}
	return err
}

// fields calls fn for each field in a protobuf message. v is the raw field
// payload: the bytes of a length-delimited field, or the encoded scalar,
// which varint and fixed64 decode.
func fields(b []byte, fn func(protowire.Number, protowire.Type, []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return errMalformed
		}
		b = b[n:]
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return errMalformed
		}
		v := b[:m]
		if typ == protowire.BytesType {
			var k int
			v, k = protowire.ConsumeBytes(b)
			if k < 0 {
				return errMalformed
			}
		}
		if err := fn(num, typ, v); err != nil {
			return err
		}
		b = b[m:]
	}
	return nil
}

func varint(b []byte) uint64  { v, _ := protowire.ConsumeVarint(b); return v }
func fixed64(b []byte) uint64 { v, _ := protowire.ConsumeFixed64(b); return v }
