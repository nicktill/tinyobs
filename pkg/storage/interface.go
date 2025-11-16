package storage

import (
	"context"
	"time"

	"tinyobs/pkg/sdk/metrics"
)

// Storage defines the interface for metric storage backends.
// Implementations: memory (testing), badger (production), objectstore (long-term)
type Storage interface {
	// Write stores metrics
	Write(ctx context.Context, metrics []metrics.Metric) error

	// Query retrieves metrics within a time range
	Query(ctx context.Context, req QueryRequest) ([]metrics.Metric, error)

	// Delete removes metrics older than the given time
	Delete(ctx context.Context, before time.Time) error

	// Close cleanly shuts down the storage
	Close() error

	// Stats returns storage statistics
	Stats(ctx context.Context) (*Stats, error)
}

// QueryRequest specifies what metrics to retrieve
type QueryRequest struct {
	// Time range
	Start time.Time
	End   time.Time

	// Filter by metric name (optional)
	MetricNames []string

	// Filter by labels (optional)
	Labels map[string]string

	// Limit number of results (0 = no limit)
	Limit int
}

// Stats provides storage health and usage info
type Stats struct {
	// Total metrics stored
	TotalMetrics uint64

	// Unique time series (metric name + label combinations)
	TotalSeries uint64

	// Storage size in bytes
	SizeBytes uint64

	// Oldest metric timestamp
	OldestMetric time.Time

	// Newest metric timestamp
	NewestMetric time.Time
}
