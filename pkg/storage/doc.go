/*
Package storage provides the pluggable storage abstraction for TinyObs metrics.

# Storage Interface

TinyObs uses an interface-based design to support multiple storage backends:
  - memory: In-memory storage for testing and ephemeral workloads
  - badger: BadgerDB (LSM tree + Snappy compression) for persistent storage
  - objectstore: (Future) S3/GCS for long-term archival

All backends implement the Storage interface:

	type Storage interface {
	    Write(ctx context.Context, metrics []metrics.Metric) error
	    Query(ctx context.Context, req QueryRequest) ([]metrics.Metric, error)
	    Delete(ctx context.Context, opts DeleteOptions) error
	    Stats(ctx context.Context) (*Stats, error)
	    Close() error
	}

# Why Pluggable Storage?

Different use cases need different backends:

  - Development: Memory backend (fast, no disk I/O)
  - Production: BadgerDB (persistent, compressed, fast writes)
  - Testing: Memory backend (no cleanup, fast teardown)
  - Long-term: Object storage (cheap, infinite retention)

By abstracting storage, you can switch backends without changing application code.

# Resolution Levels

TinyObs stores metrics at three resolutions:

  - Raw (ResolutionRaw): Original data points (retained 14 days)
  - 5-minute (Resolution5m): Aggregates every 5 minutes (retained 90 days)
  - 1-hour (Resolution1h): Aggregates every hour (retained 1 year)

Resolution is stored as a special label "__resolution__":
  - Raw metrics: no __resolution__ label
  - 5m aggregates: __resolution__="5m"
  - 1h aggregates: __resolution__="1h"

This allows querying specific resolution levels using label filters.

# Usage Example

	import (
	    "context"
	    "github.com/nicktill/tinyobs/pkg/storage/badger"
	)

	// Create storage
	store, err := badger.New(badger.Config{Path: "./data"})
	if err != nil {
	    log.Fatal(err)
	}
	defer store.Close()

	// Write metrics
	metrics := []metrics.Metric{
	    {Name: "cpu_usage", Value: 75.5, Timestamp: time.Now()},
	}
	err = store.Write(context.Background(), metrics)

	// Query metrics
	results, err := store.Query(context.Background(), storage.QueryRequest{
	    Start:       time.Now().Add(-1 * time.Hour),
	    End:         time.Now(),
	    MetricNames: []string{"cpu_usage"},
	})

	// Get statistics
	stats, err := store.Stats(context.Background())
	fmt.Printf("Total metrics: %d\n", stats.TotalMetrics)
	fmt.Printf("Total series: %d\n", stats.TotalSeries)
	fmt.Printf("Storage size: %d bytes\n", stats.SizeBytes)

# Query Filtering

QueryRequest supports several filters:

	// Filter by time range only
	req := QueryRequest{
	    Start: time.Now().Add(-24 * time.Hour),
	    End:   time.Now(),
	}

	// Filter by metric names
	req := QueryRequest{
	    Start:       startTime,
	    End:         endTime,
	    MetricNames: []string{"cpu_usage", "memory_usage"},
	}

	// Filter by labels
	req := QueryRequest{
	    Start:  startTime,
	    End:    endTime,
	    Labels: map[string]string{"service": "api", "env": "prod"},
	}

	// Limit results
	req := QueryRequest{
	    Start: startTime,
	    End:   endTime,
	    Limit: 1000,  // Return max 1000 metrics
	}

# Retention & Deletion

Use DeleteOptions to implement retention policies:

	// Delete all raw metrics older than 14 days
	rawRes := storage.ResolutionRaw
	store.Delete(ctx, storage.DeleteOptions{
	    Before:     time.Now().Add(-14 * 24 * time.Hour),
	    Resolution: &rawRes,
	})

	// Delete all metrics (all resolutions) older than 1 year
	store.Delete(ctx, storage.DeleteOptions{
	    Before:     time.Now().Add(-365 * 24 * time.Hour),
	    Resolution: nil,  // nil = all resolutions
	})

# Performance Characteristics

Different backends have different performance profiles:

Memory backend:
  - Writes: 500k+ metrics/sec
  - Queries: <1ms for 1000 points
  - Storage: Unlimited (until RAM full)
  - Persistence: None (data lost on restart)

BadgerDB backend:
  - Writes: 50k+ metrics/sec (M1 MacBook Pro)
  - Queries: <100ms for 1000 points
  - Storage: ~70 MB after 1 year (10 series, 1 sample/sec)
  - Persistence: Durable (survives restarts)

# Best Practices

1. Always call Close() when done to flush pending writes
2. Use context.WithTimeout() to prevent hung queries
3. Monitor Stats() to track cardinality and storage growth
4. Use resolution filters to avoid querying all resolutions
5. Batch writes when possible (pass []Metric instead of single metrics)

# See Also

  - memory.New() for in-memory storage
  - badger.New() for persistent BadgerDB storage
  - pkg/compaction for downsampling logic
*/
package storage
