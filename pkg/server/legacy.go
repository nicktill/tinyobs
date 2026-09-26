package server

// Deprecated V1 endpoints, kept so the V1 dashboard and the V1 SDK keep
// working while V2 lands. They are backed by the new storage and query
// engine, and are removed together with the V1 UI and SDK.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

const legacyMaxBody = 16 << 20

func (s *Server) registerLegacy(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/ingest", s.legacyIngest)
	mux.HandleFunc("GET /v1/query", s.legacyLatest)
	mux.HandleFunc("GET /v1/query/range", s.legacyRange)
	mux.HandleFunc("POST /v1/query/execute", s.legacyExecute)
	mux.HandleFunc("GET /v1/stats", s.legacyStats)
	mux.HandleFunc("GET /v1/storage", s.legacyStorage)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "healthy", "version": Version})
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// legacyIngest accepts the V1 SDK's JSON batches. The V1 SDK sends a sample
// per increment, so a batch can hold several samples for one series in the
// same millisecond; the last one wins, since counters only grow.
func (s *Server) legacyIngest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Metrics []metrics.Metric `json:"metrics"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, legacyMaxBody)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	type key struct {
		series string
		t      int64
	}
	type entry struct {
		lset labels.Labels
		t    int64
		v    float64
	}
	latest := map[key]entry{}
	now := time.Now().UnixMilli()
	for _, m := range req.Metrics {
		lm := make(map[string]string, len(m.Labels)+1)
		for k, v := range m.Labels {
			lm[k] = v
		}
		lm[labels.MetricName] = m.Name
		ls := labels.FromMap(lm)
		t := now
		if !m.Timestamp.IsZero() {
			t = m.Timestamp.UnixMilli()
		}
		latest[key{ls.String(), t}] = entry{ls, t, m.Value}
		if m.Type != "" {
			family := m.Name
			typ := string(m.Type)
			if m.Type == metrics.HistogramType {
				for _, suffix := range []string{"_bucket", "_sum", "_count"} {
					if len(family) > len(suffix) && family[len(family)-len(suffix):] == suffix {
						family = family[:len(family)-len(suffix)]
					}
				}
			}
			s.db.SetMetadata(family, tsdb.Metadata{Type: typ})
		}
	}
	entries := make([]entry, 0, len(latest))
	for _, e := range latest {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].t < entries[j].t })
	app := s.db.Appender()
	for _, e := range entries {
		app.Append(e.lset, e.t, e.v)
	}
	res, err := app.Commit()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := map[string]any{"status": "success", "count": res.Appended}
	if res.NumRejected() > 0 {
		resp["rejected"] = res.NumRejected()
		resp["message"] = res.FirstError.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

type legacyMetric struct {
	Name      string            `json:"name"`
	Type      string            `json:"type,omitempty"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

func legacyTimes(r *http.Request) (time.Time, time.Time, error) {
	parse := func(name string, def time.Time) (time.Time, error) {
		v := r.URL.Query().Get(name)
		if v == "" {
			return def, nil
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid %s: %w", name, err)
		}
		return t, nil
	}
	now := time.Now()
	start, err := parse("start", now.Add(-time.Hour))
	if err != nil {
		return start, start, err
	}
	end, err := parse("end", now)
	return start, end, err
}

// legacyLatest returns the newest sample of every series in the window.
func (s *Server) legacyLatest(w http.ResponseWriter, r *http.Request) {
	start, end, err := legacyTimes(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	series, err := s.db.Select(ctx, start.UnixMilli(), end.UnixMilli(), 5_000_000,
		labels.MustNewMatcher(labels.MatchRegexp, labels.MetricName, ".+"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	meta := s.db.Metadata()
	out := make([]legacyMetric, 0, len(series))
	for _, ser := range series {
		last := ser.Samples[len(ser.Samples)-1]
		if tsdb.IsStaleNaN(last.V) {
			continue
		}
		name := ser.Labels.Get(labels.MetricName)
		out = append(out, legacyMetric{
			Name:      name,
			Type:      meta[name].Type,
			Value:     last.V,
			Labels:    ser.Labels.DropMetricName().Map(),
			Timestamp: time.UnixMilli(last.T),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": out, "count": len(out)})
}

// legacyRange returns one metric's series, thinned to maxPoints per series.
func (s *Server) legacyRange(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("metric")
	if name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("metric parameter is required"))
		return
	}
	start, end, err := legacyTimes(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	maxPoints := 1000
	if v := r.URL.Query().Get("maxPoints"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			maxPoints = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	series, err := s.db.Select(ctx, start.UnixMilli(), end.UnixMilli(), 5_000_000,
		labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type point struct {
		T int64   `json:"t"`
		V float64 `json:"v"`
	}
	type seriesData struct {
		Metric     string            `json:"metric"`
		Labels     map[string]string `json:"labels,omitempty"`
		Points     []point           `json:"points"`
		Resolution string            `json:"resolution"`
	}
	out := []seriesData{}
	for _, ser := range series {
		stride := int(math.Ceil(float64(len(ser.Samples)) / float64(maxPoints)))
		var pts []point
		for i := 0; i < len(ser.Samples); i += stride {
			if smp := ser.Samples[i]; !tsdb.IsStaleNaN(smp.V) {
				pts = append(pts, point{smp.T, smp.V})
			}
		}
		out = append(out, seriesData{Metric: name, Labels: ser.Labels.DropMetricName().Map(), Points: pts, Resolution: "raw"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// legacyExecute runs a PromQL range query from the V1 query editor.
func (s *Server) legacyExecute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string    `json:"query"`
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
		Step  string    `json:"step"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	now := time.Now()
	if req.End.IsZero() {
		req.End = now
	}
	if req.Start.IsZero() {
		req.Start = req.End.Add(-time.Hour)
	}
	step := 15 * time.Second
	if req.Step != "" {
		d, err := time.ParseDuration(req.Step)
		if err != nil || d <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid step %q", req.Step))
			return
		}
		step = d
	}
	m, err := s.engine.Range(r.Context(), req.Query, req.Start, req.End, step)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error(), "query": req.Query})
		return
	}
	type result struct {
		Metric map[string]string `json:"metric"`
		Values [][]any           `json:"values"`
	}
	res := make([]result, len(m))
	for i, ser := range m {
		vals := make([][]any, len(ser.Points))
		for j, p := range ser.Points {
			vals[j] = []any{float64(p.T) / 1000, strconv.FormatFloat(p.F, 'f', -1, 64)}
		}
		res[i] = result{ser.Metric.Map(), vals}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"query":  req.Query,
		"data":   map[string]any{"resultType": "matrix", "result": res},
	})
}

func (s *Server) legacyStats(w http.ResponseWriter, _ *http.Request) {
	st := s.db.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"TotalMetrics": st.SamplesAppended,
		"TotalSeries":  st.NumSeries,
		"SizeBytes":    st.DiskBytes,
		"OldestMetric": time.UnixMilli(st.MinTime),
		"NewestMetric": time.UnixMilli(st.MaxTime),
	})
}

func (s *Server) legacyStorage(w http.ResponseWriter, _ *http.Request) {
	st := s.db.Stats()
	// V2 bounds disk by retention and series count rather than a byte limit;
	// report the upper bound implied by them so the V1 gauge stays meaningful.
	bound := int64(st.MaxSeries) * int64(st.Retention/(15*time.Second)) * 14
	writeJSON(w, http.StatusOK, map[string]int64{"used_bytes": st.DiskBytes, "max_bytes": bound})
}
