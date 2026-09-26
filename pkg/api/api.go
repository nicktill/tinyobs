// Package api serves the Prometheus HTTP API (the subset TinyObs supports),
// so Grafana, promtool-style clients and the TinyObs UI can query it.
//
// Request parameters, response shapes, error types and status codes follow
// https://prometheus.io/docs/prometheus/latest/querying/api/. Invalid input is
// a 400 with errorType "bad_data"; nothing silently falls back to a default.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/promql"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

// BuildInfo describes the running binary.
type BuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildUser string `json:"buildUser"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

// API serves the query endpoints.
type API struct {
	DB        *tsdb.DB
	Engine    *promql.Engine
	BuildInfo BuildInfo
	Now       func() time.Time
}

// Register adds the API routes to mux.
func (a *API) Register(mux *http.ServeMux) {
	for _, m := range []string{"GET", "POST"} {
		mux.HandleFunc(m+" /api/v1/query", a.query)
		mux.HandleFunc(m+" /api/v1/query_range", a.queryRange)
		mux.HandleFunc(m+" /api/v1/series", a.series)
		mux.HandleFunc(m+" /api/v1/labels", a.labelNames)
	}
	mux.HandleFunc("GET /api/v1/label/{name}/values", a.labelValues)
	mux.HandleFunc("GET /api/v1/metadata", a.metadata)
	mux.HandleFunc("GET /api/v1/status/buildinfo", a.buildInfo)
	mux.HandleFunc("GET /api/v1/status/tsdb", a.tsdbStatus)
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

type errorType string

const (
	errBadData     errorType = "bad_data"
	errExec        errorType = "execution"
	errTimeout     errorType = "timeout"
	errCanceled    errorType = "canceled"
	errInternal    errorType = "internal"
	errUnavailable errorType = "unavailable"
)

type response struct {
	Status    string    `json:"status"`
	Data      any       `json:"data,omitempty"`
	ErrorType errorType `json:"errorType,omitempty"`
	Error     string    `json:"error,omitempty"`
}

func respond(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Status: "success", Data: data})
}

func respondError(w http.ResponseWriter, typ errorType, err error) {
	code := http.StatusBadRequest
	switch typ {
	case errExec:
		code = http.StatusUnprocessableEntity
	case errTimeout, errUnavailable:
		code = http.StatusServiceUnavailable
	case errCanceled:
		code = 499
	case errInternal:
		code = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(response{Status: "error", ErrorType: typ, Error: err.Error()})
}

// queryError maps an engine error to the Prometheus error type.
func queryError(w http.ResponseWriter, err error) {
	var pe *promql.ParseError
	switch {
	case errors.As(err, &pe):
		respondError(w, errBadData, err)
	case errors.Is(err, promql.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		respondError(w, errTimeout, err)
	case errors.Is(err, context.Canceled):
		respondError(w, errCanceled, err)
	default:
		respondError(w, errExec, err)
	}
}

func (a *API) query(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	q := r.Form.Get("query")
	if q == "" {
		respondError(w, errBadData, errors.New("missing query parameter"))
		return
	}
	ts, err := parseTimeParam(r, "time", a.now())
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	ctx, cancel, err := withTimeout(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	defer cancel()
	v, err := a.Engine.Instant(ctx, q, ts)
	if err != nil {
		queryError(w, err)
		return
	}
	respond(w, queryData(v))
}

func (a *API) queryRange(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	q := r.Form.Get("query")
	if q == "" {
		respondError(w, errBadData, errors.New("missing query parameter"))
		return
	}
	start, err := parseTime(r.Form.Get("start"))
	if err != nil {
		respondError(w, errBadData, fmt.Errorf("invalid parameter \"start\": %w", err))
		return
	}
	end, err := parseTime(r.Form.Get("end"))
	if err != nil {
		respondError(w, errBadData, fmt.Errorf("invalid parameter \"end\": %w", err))
		return
	}
	if end.Before(start) {
		respondError(w, errBadData, errors.New("end timestamp must not be before start time"))
		return
	}
	step, err := parseDuration(r.Form.Get("step"))
	if err != nil {
		respondError(w, errBadData, fmt.Errorf("invalid parameter \"step\": %w", err))
		return
	}
	if step <= 0 {
		respondError(w, errBadData, errors.New("zero or negative query resolution step widths are not accepted. Try a positive integer"))
		return
	}
	ctx, cancel, err := withTimeout(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	defer cancel()
	m, err := a.Engine.Range(ctx, q, start, end, step)
	if err != nil {
		queryError(w, err)
		return
	}
	respond(w, queryData(m))
}

func withTimeout(r *http.Request) (context.Context, context.CancelFunc, error) {
	t := r.Form.Get("timeout")
	if t == "" {
		ctx, cancel := context.WithCancel(r.Context())
		return ctx, cancel, nil
	}
	d, err := parseDuration(t)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid parameter \"timeout\": %w", err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), d)
	return ctx, cancel, nil
}

// matchersParam parses the match[] parameters into selectors.
func matchersParam(r *http.Request, required bool) ([][]*labels.Matcher, error) {
	var out [][]*labels.Matcher
	for _, s := range r.Form["match[]"] {
		e, err := promql.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid parameter \"match[]\": %w", err)
		}
		vs, ok := e.(*promql.VectorSelector)
		if !ok {
			return nil, fmt.Errorf("invalid parameter \"match[]\": %q is not a series selector", s)
		}
		out = append(out, vs.Matchers)
	}
	if required && len(out) == 0 {
		return nil, errors.New("no match[] parameter provided")
	}
	return out, nil
}

// timeRange reads start and end, defaulting to all time.
func timeRange(r *http.Request) (int64, int64, error) {
	mint, maxt := int64(math.MinInt64), int64(math.MaxInt64)
	if s := r.Form.Get("start"); s != "" {
		t, err := parseTime(s)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid parameter \"start\": %w", err)
		}
		mint = t.UnixMilli()
	}
	if s := r.Form.Get("end"); s != "" {
		t, err := parseTime(s)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid parameter \"end\": %w", err)
		}
		maxt = t.UnixMilli()
	}
	return mint, maxt, nil
}

func limitParam(r *http.Request) (int, error) {
	s := r.Form.Get("limit")
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid parameter \"limit\": %q", s)
	}
	return n, nil
}

func (a *API) series(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	sets, err := matchersParam(r, true)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	mint, maxt, err := timeRange(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	limit, err := limitParam(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	seen := map[string]bool{}
	var all []labels.Labels
	for _, ms := range sets {
		for _, ls := range a.DB.Series(mint, maxt, ms...) {
			if k := ls.String(); !seen[k] {
				seen[k] = true
				all = append(all, ls)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return labels.Compare(all[i], all[j]) < 0 })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]map[string]string, len(all))
	for i, ls := range all {
		out[i] = ls.Map()
	}
	respond(w, out)
}

func (a *API) labelNames(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	a.stringList(w, r, func(mint, maxt int64, ms []*labels.Matcher) []string {
		return a.DB.LabelNames(mint, maxt, ms...)
	})
}

func (a *API) labelValues(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	name := r.PathValue("name")
	if name != labels.MetricName && !labels.IsValidLabelName(name) {
		respondError(w, errBadData, fmt.Errorf("invalid label name: %q", name))
		return
	}
	a.stringList(w, r, func(mint, maxt int64, ms []*labels.Matcher) []string {
		return a.DB.LabelValues(name, mint, maxt, ms...)
	})
}

// stringList serves labels and label values: the union over each match[]
// selector, or over all series when none is given.
func (a *API) stringList(w http.ResponseWriter, r *http.Request, get func(int64, int64, []*labels.Matcher) []string) {
	sets, err := matchersParam(r, false)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	mint, maxt, err := timeRange(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	limit, err := limitParam(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	if len(sets) == 0 {
		sets = [][]*labels.Matcher{nil}
	}
	set := map[string]struct{}{}
	for _, ms := range sets {
		for _, v := range get(mint, maxt, ms) {
			set[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	respond(w, out)
}

type metadataEntry struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

func (a *API) metadata(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	limit, err := limitParam(r)
	if err != nil {
		respondError(w, errBadData, err)
		return
	}
	metric := r.Form.Get("metric")
	out := map[string][]metadataEntry{}
	names := []string{}
	all := a.DB.Metadata()
	for name := range all {
		if metric == "" || metric == name {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if limit > 0 && len(out) >= limit {
			break
		}
		md := all[name]
		out[name] = []metadataEntry{{Type: md.Type, Help: md.Help, Unit: md.Unit}}
	}
	respond(w, out)
}

func (a *API) buildInfo(w http.ResponseWriter, _ *http.Request) {
	bi := a.BuildInfo
	if bi.GoVersion == "" {
		bi.GoVersion = runtime.Version()
	}
	respond(w, bi)
}

type nameValue struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func (a *API) tsdbStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respondError(w, errBadData, err)
		return
	}
	limit := 10
	if s := r.Form.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			respondError(w, errBadData, fmt.Errorf("invalid parameter \"limit\": %q", s))
			return
		}
		limit = n
	}
	st := a.DB.Stats()
	c := a.DB.Cardinality(limit)
	conv := func(cs []tsdb.Count) []nameValue {
		out := make([]nameValue, len(cs))
		for i, x := range cs {
			out[i] = nameValue{x.Name, x.Value}
		}
		return out
	}
	respond(w, map[string]any{
		"headStats": map[string]any{
			"numSeries":  st.NumSeries,
			"chunkCount": 0,
			"minTime":    st.MinTime,
			"maxTime":    st.MaxTime,
		},
		"seriesCountByMetricName":     conv(c.SeriesCountByMetricName),
		"labelValueCountByLabelName":  conv(c.LabelValueCountByLabelName),
		"memoryInBytesByLabelName":    []nameValue{},
		"seriesCountByLabelValuePair": conv(c.SeriesCountByLabelValuePair),
	})
}

// Result encoding.

type queryResult struct {
	ResultType promql.ValueType `json:"resultType"`
	Result     any              `json:"result"`
}

type vectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

type matrixSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
}

func queryData(v promql.Value) queryResult {
	switch x := v.(type) {
	case promql.Scalar:
		return queryResult{promql.TypeScalar, [2]any{jsonTime(x.T), formatFloat(x.V)}}
	case promql.String:
		return queryResult{promql.TypeString, [2]any{jsonTime(x.T), x.V}}
	case promql.Vector:
		out := make([]vectorSample, len(x))
		for i, s := range x {
			out[i] = vectorSample{s.Metric.Map(), [2]any{jsonTime(s.T), formatFloat(s.F)}}
		}
		return queryResult{promql.TypeVector, out}
	case promql.Matrix:
		out := make([]matrixSeries, len(x))
		for i, s := range x {
			vals := make([][2]any, len(s.Points))
			for j, p := range s.Points {
				vals[j] = [2]any{jsonTime(p.T), formatFloat(p.F)}
			}
			out[i] = matrixSeries{s.Metric.Map(), vals}
		}
		return queryResult{promql.TypeMatrix, out}
	}
	return queryResult{}
}

// jsonTime renders a millisecond timestamp as Unix seconds, as Prometheus does.
type jsonTime int64

func (t jsonTime) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, float64(t)/1000, 'f', -1, 64), nil
}

func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Parameter parsing.

func parseTimeParam(r *http.Request, name string, def time.Time) (time.Time, error) {
	s := r.Form.Get(name)
	if s == "" {
		return def, nil
	}
	t, err := parseTime(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid parameter %q: %w", name, err)
	}
	return t, nil
}

// parseTime accepts Unix seconds (possibly fractional) or RFC 3339.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("missing value")
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e12 {
			return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
		}
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(math.Round(frac*1000))*int64(time.Millisecond)).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q to a valid timestamp", s)
}

// parseDuration accepts float seconds or a Prometheus duration such as 15s.
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("missing value")
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		d := f * float64(time.Second)
		if d >= math.MaxInt64 || d <= math.MinInt64 || math.IsNaN(d) {
			return 0, fmt.Errorf("cannot parse %q to a valid duration: out of range", s)
		}
		return time.Duration(d), nil
	}
	if d, err := promql.ParseDuration(s); err == nil {
		return d, nil
	}
	return 0, fmt.Errorf("cannot parse %q to a valid duration", s)
}
