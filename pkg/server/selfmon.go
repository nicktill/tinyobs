package server

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// selfMonitor records TinyObs's own metrics. They are written into its own
// storage as job="tinyobs" (so the UI's System page is ordinary PromQL) and
// served at /metrics in the Prometheus text format for external scrapers.
type selfMonitor struct {
	db       *tsdb.DB
	instance string
	start    time.Time

	httpRequests *metrics.Counter
	httpDuration *metrics.Histogram
}

func newSelfMonitor(db *tsdb.DB, instance string) *selfMonitor {
	return &selfMonitor{
		db:           db,
		instance:     instance,
		start:        time.Now(),
		httpRequests: metrics.NewCounter("tinyobs_http_requests_total"),
		httpDuration: metrics.NewHistogram("tinyobs_http_request_duration_seconds", nil),
	}
}

// instrument counts requests and their latency per route pattern.
func (m *selfMonitor) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		handler := r.Pattern // set by ServeMux to the matched route, e.g. "GET /api/v1/query"
		if handler == "" {
			handler = "other"
		}
		if i := strings.IndexByte(handler, ' '); i >= 0 {
			handler = handler[i+1:]
		}
		m.httpRequests.Inc("handler", handler, "code", strconv.Itoa(sw.code))
		m.httpDuration.Observe(time.Since(start).Seconds(), "handler", handler)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type family struct {
	name, typ, help string
	samples         []selfSample
}

type selfSample struct {
	suffix string
	labels []metrics.Label
	value  float64
}

// collect gathers the current value of every self-metric.
func (m *selfMonitor) collect() []family {
	st := m.db.Stats()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	gauge := func(name, help string, v float64) family {
		return family{name, "gauge", help, []selfSample{{value: v}}}
	}
	rejected := family{name: "tinyobs_samples_rejected_total", typ: "counter", help: "Samples not stored, by reason."}
	for _, reason := range []string{tsdb.ReasonOutOfOrder, tsdb.ReasonSeriesCap, tsdb.ReasonInvalid} {
		rejected.samples = append(rejected.samples, selfSample{labels: []metrics.Label{{Name: "reason", Value: reason}}, value: float64(st.SamplesRejected[reason])})
	}
	fams := []family{
		{"tinyobs_build_info", "gauge", "TinyObs version.", []selfSample{{labels: []metrics.Label{{Name: "goversion", Value: runtime.Version()}, {Name: "version", Value: Version}}, value: 1}}},
		gauge("tinyobs_series", "Series currently stored.", float64(st.NumSeries)),
		gauge("tinyobs_series_limit", "Maximum number of series.", float64(st.MaxSeries)),
		gauge("tinyobs_disk_bytes", "Disk space used by the data directory.", float64(st.DiskBytes)),
		gauge("tinyobs_retention_seconds", "How long samples are kept.", st.Retention.Seconds()),
		{"tinyobs_samples_appended_total", "counter", "Samples stored since start.", []selfSample{{value: float64(st.SamplesAppended)}}},
		rejected,
		gauge("go_goroutines", "Number of goroutines.", float64(runtime.NumGoroutine())),
		gauge("go_memstats_heap_alloc_bytes", "Heap bytes allocated and in use.", float64(ms.HeapAlloc)),
		gauge("go_memstats_sys_bytes", "Bytes obtained from the OS.", float64(ms.Sys)),
		gauge("process_start_time_seconds", "Start time of the process.", float64(m.start.Unix())),
	}
	req := family{name: "tinyobs_http_requests_total", typ: "counter", help: "HTTP requests by route and status code."}
	for _, p := range m.httpRequests.Snapshot() {
		req.samples = append(req.samples, selfSample{labels: p.Labels, value: p.Value})
	}
	fams = append(fams, req)
	dur := family{name: "tinyobs_http_request_duration_seconds", typ: "histogram", help: "HTTP request latency by route."}
	for _, p := range m.httpDuration.Snapshot() {
		var cum uint64
		for i, b := range m.httpDuration.Bounds() {
			cum += p.BucketCounts[i]
			dur.samples = append(dur.samples, selfSample{"_bucket", append(append([]metrics.Label{}, p.Labels...), metrics.Label{Name: "le", Value: strconv.FormatFloat(b, 'f', -1, 64)}), float64(cum)})
		}
		dur.samples = append(dur.samples,
			selfSample{"_bucket", append(append([]metrics.Label{}, p.Labels...), metrics.Label{Name: "le", Value: "+Inf"}), float64(p.Count)},
			selfSample{"_sum", p.Labels, p.Sum},
			selfSample{"_count", p.Labels, float64(p.Count)},
		)
	}
	fams = append(fams, dur)
	return fams
}

// record writes the self-metrics into storage.
func (m *selfMonitor) record(now time.Time) error {
	app := m.db.Appender()
	t := now.UnixMilli()
	for _, f := range m.collect() {
		m.db.SetMetadata(f.name, tsdb.Metadata{Type: f.typ, Help: f.help})
		for _, s := range f.samples {
			ls := []labels.Label{
				{Name: labels.MetricName, Value: f.name + s.suffix},
				{Name: "job", Value: "tinyobs"},
				{Name: "instance", Value: m.instance},
			}
			for _, l := range s.labels {
				ls = append(ls, labels.Label{Name: l.Name, Value: l.Value})
			}
			app.Append(labels.New(ls...), t, s.value)
		}
	}
	_, err := app.Commit()
	return err
}

// ServeHTTP serves /metrics in the Prometheus text format.
func (m *selfMonitor) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeExposition(w, m.collect())
}

func writeExposition(w io.Writer, fams []family) {
	for _, f := range fams {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, f.typ)
		for _, s := range f.samples {
			ls := append([]metrics.Label(nil), s.labels...)
			sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
			var parts []string
			for _, l := range ls {
				parts = append(parts, l.Name+`="`+labelEscaper.Replace(l.Value)+`"`)
			}
			lstr := ""
			if len(parts) > 0 {
				lstr = "{" + strings.Join(parts, ",") + "}"
			}
			fmt.Fprintf(w, "%s%s%s %s\n", f.name, s.suffix, lstr, formatValue(s.value))
		}
	}
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
