package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/tsdb"
)

const maxIngestBody = 16 << 20

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// ingest accepts the Go SDK's JSON batches at POST /v1/ingest. Older SDK
// versions sent a sample per increment, so a batch can hold several samples
// for one series in the same millisecond; the last one wins, since counters
// only grow.
func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Metrics []metrics.Metric `json:"metrics"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIngestBody)).Decode(&req); err != nil {
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
