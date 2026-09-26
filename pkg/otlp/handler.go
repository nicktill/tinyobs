package otlp

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// MaxBodyBytes caps a request body after decompression.
const MaxBodyBytes = 16 << 20

// Handler serves POST /v1/metrics.
type Handler struct {
	DB  *tsdb.DB
	Now func() time.Time
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	isJSON := mt == "application/json"
	if mt != "application/x-protobuf" && !isJSON {
		writeStatus(w, isJSON, http.StatusUnsupportedMediaType, "unsupported content type %q; use application/x-protobuf or application/json", mt)
		return
	}

	body := io.Reader(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	switch r.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(body)
		if err != nil {
			writeStatus(w, isJSON, http.StatusBadRequest, "invalid gzip body: %v", err)
			return
		}
		defer zr.Close()
		body = zr
	default:
		writeStatus(w, isJSON, http.StatusUnsupportedMediaType, "unsupported content encoding %q", r.Header.Get("Content-Encoding"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(body, MaxBodyBytes+1))
	if err != nil {
		code := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			code = http.StatusRequestEntityTooLarge
		}
		writeStatus(w, isJSON, code, "reading body: %v", err)
		return
	}
	if len(data) > MaxBodyBytes {
		writeStatus(w, isJSON, http.StatusRequestEntityTooLarge, "request body exceeds 16 MiB after decompression")
		return
	}

	var rms []resourceMetrics
	if isJSON {
		rms, err = decodeJSON(data)
	} else {
		rms, err = decodeProto(data)
	}
	if err != nil {
		writeStatus(w, isJSON, http.StatusBadRequest, "decoding OTLP request: %v", err)
		return
	}

	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	tr := translate(rms, now().UnixMilli())

	// Samples are stored in time order so a request carrying several points
	// per series is accepted regardless of the order the exporter used.
	sort.SliceStable(tr.samples, func(i, j int) bool { return tr.samples[i].t < tr.samples[j].t })
	app := h.DB.Appender()
	for _, s := range tr.samples {
		app.Append(s.lset, s.t, s.v)
	}
	res, err := app.Commit()
	if err != nil {
		// Storage failures are transient from the client's point of view;
		// 503 tells OTLP exporters to retry.
		writeStatus(w, isJSON, http.StatusServiceUnavailable, "storing samples: %v", err)
		return
	}
	for name, md := range tr.metadata {
		h.DB.SetMetadata(name, md)
	}

	rejected := tr.rejected + res.NumRejected()
	msgs := tr.errs
	if res.FirstError != nil {
		msgs = append(msgs, res.FirstError.Error())
	}
	writeResponse(w, isJSON, rejected, strings.Join(msgs, "; "))
}

// writeResponse writes an ExportMetricsServiceResponse, with partial_success
// set when data points were rejected.
func writeResponse(w http.ResponseWriter, isJSON bool, rejected int, msg string) {
	if isJSON {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{}
		if rejected > 0 {
			resp["partialSuccess"] = map[string]string{
				"rejectedDataPoints": strconv.Itoa(rejected),
				"errorMessage":       msg,
			}
		}
		json.NewEncoder(w).Encode(resp)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	var b []byte
	if rejected > 0 {
		var ps []byte
		ps = protowire.AppendTag(ps, 1, protowire.VarintType)
		ps = protowire.AppendVarint(ps, uint64(rejected))
		ps = protowire.AppendTag(ps, 2, protowire.BytesType)
		ps = protowire.AppendString(ps, msg)
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, ps)
	}
	w.Write(b)
}

// writeStatus writes a google.rpc.Status error, as the OTLP/HTTP spec asks.
func writeStatus(w http.ResponseWriter, isJSON bool, code int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	rpcCode := 3 // INVALID_ARGUMENT
	if code == http.StatusServiceUnavailable {
		rpcCode = 14 // UNAVAILABLE
	}
	if isJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{"code": rpcCode, "message": msg})
		return
	}
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(rpcCode))
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendString(b, msg)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(code)
	w.Write(b)
}
