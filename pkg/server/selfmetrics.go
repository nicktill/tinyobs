package server

import (
	"fmt"
	"net/http"
	"sort"
)

// selfMetrics serves TinyObs's own health in the Prometheus text format, so
// it can be scraped (by Prometheus, or by an agent that remote-writes back
// here). These are the numbers to alert on: rejected samples mean data is
// being dropped, and series near the limit mean it soon will be.
func (s *Server) selfMetrics(w http.ResponseWriter, _ *http.Request) {
	st := s.db.Stats()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	gauge := func(name, help string, v float64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %g\n", name, help, name, name, v)
	}
	fmt.Fprintf(w, "# HELP tinyobs_build_info TinyObs version.\n# TYPE tinyobs_build_info gauge\ntinyobs_build_info{version=%q} 1\n", Version)
	gauge("tinyobs_series", "Series in the index.", float64(st.NumSeries))
	gauge("tinyobs_series_limit", "Maximum number of series (TINYOBS_MAX_SERIES).", float64(st.MaxSeries))
	gauge("tinyobs_series_per_metric_limit", "Maximum series per metric name (TINYOBS_MAX_SERIES_PER_METRIC).", float64(st.MaxSeriesPerMetric))
	gauge("tinyobs_retention_seconds", "Configured retention.", st.Retention.Seconds())
	gauge("tinyobs_storage_bytes", "Disk space used by the data directory.", float64(st.DiskBytes))
	if st.MaxTime != 0 {
		gauge("tinyobs_oldest_sample_timestamp_seconds", "Timestamp of the oldest stored sample.", float64(st.MinTime)/1000)
	}
	fmt.Fprintf(w, "# HELP tinyobs_samples_appended_total Samples stored since start.\n# TYPE tinyobs_samples_appended_total counter\ntinyobs_samples_appended_total %d\n", st.SamplesAppended)
	fmt.Fprintf(w, "# HELP tinyobs_samples_rejected_total Samples dropped since start, by reason.\n# TYPE tinyobs_samples_rejected_total counter\n")
	reasons := []string{"out_of_order", "series_limit", "metric_series_limit", "invalid_labels"}
	for r := range st.SamplesRejected {
		if !contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(w, "tinyobs_samples_rejected_total{reason=%q} %d\n", r, st.SamplesRejected[r])
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
