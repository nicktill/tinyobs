package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"tinyobs/pkg/sdk/metrics"
	"tinyobs/pkg/storage"
)

// Storage stores metrics in memory. Data is lost on restart.
// Useful for testing and development.
type Storage struct {
	metrics []metrics.Metric
	mu      sync.RWMutex
}

// New creates an in-memory storage backend
func New() *Storage {
	return &Storage{
		metrics: make([]metrics.Metric, 0, 10000),
	}
}

// Write stores metrics in memory
func (s *Storage) Write(ctx context.Context, metrics []metrics.Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.metrics = append(s.metrics, metrics...)
	return nil
}

// Query retrieves metrics matching the request
func (s *Storage) Query(ctx context.Context, req storage.QueryRequest) ([]metrics.Metric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []metrics.Metric

	for _, m := range s.metrics {
		// Time range filter
		if m.Timestamp.Before(req.Start) || m.Timestamp.After(req.End) {
			continue
		}

		// Metric name filter
		if len(req.MetricNames) > 0 {
			found := false
			for _, name := range req.MetricNames {
				if m.Name == name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Label filter
		if len(req.Labels) > 0 {
			match := true
			for k, v := range req.Labels {
				if m.Labels == nil || m.Labels[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		results = append(results, m)

		// Limit check
		if req.Limit > 0 && len(results) >= req.Limit {
			break
		}
	}

	return results, nil
}

// Delete removes metrics older than the given time
func (s *Storage) Delete(ctx context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Filter out old metrics
	filtered := make([]metrics.Metric, 0, len(s.metrics))
	for _, m := range s.metrics {
		if m.Timestamp.After(before) {
			filtered = append(filtered, m)
		}
	}

	s.metrics = filtered
	return nil
}

// Close is a no-op for memory storage
func (s *Storage) Close() error {
	return nil
}

// Stats returns storage statistics
func (s *Storage) Stats(ctx context.Context) (*storage.Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &storage.Stats{
		TotalMetrics: uint64(len(s.metrics)),
	}

	if len(s.metrics) == 0 {
		return stats, nil
	}

	// Count unique series and find min/max timestamps in single pass
	seriesMap := make(map[string]bool)
	oldest := s.metrics[0].Timestamp
	newest := s.metrics[0].Timestamp

	for _, m := range s.metrics {
		// Track unique series
		key := seriesKey(m.Name, m.Labels)
		seriesMap[key] = true

		// Track min/max timestamps
		if m.Timestamp.Before(oldest) {
			oldest = m.Timestamp
		}
		if m.Timestamp.After(newest) {
			newest = m.Timestamp
		}
	}

	stats.TotalSeries = uint64(len(seriesMap))
	stats.OldestMetric = oldest
	stats.NewestMetric = newest

	// Rough size estimate (each metric ~100 bytes)
	stats.SizeBytes = uint64(len(s.metrics)) * 100

	return stats, nil
}

// seriesKey creates a unique key for a time series
func seriesKey(name string, labels map[string]string) string {
	// Simple approach: concatenate sorted labels
	// In production, would use hash for efficiency
	key := name
	if len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			key += "," + k + "=" + labels[k]
		}
	}
	return key
}
