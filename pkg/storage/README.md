# storage

Pluggable storage backends for metrics.

## Implementations

- **memory**: In-memory storage. Fast, but data lost on restart. Good for testing.
- **badger**: BadgerDB (LSM tree). Persists to disk. Production default.
- **objectstore**: S3/MinIO/GCS. For long-term retention and archival.

## Usage

```go
// Create storage
store, err := badger.New(badger.Config{
    Path: "./data",
})

// Write metrics
err = store.Write(ctx, metrics)

// Query metrics
results, err := store.Query(ctx, storage.QueryRequest{
    Start: time.Now().Add(-1 * time.Hour),
    End:   time.Now(),
})

// Get stats
stats, err := store.Stats(ctx)
fmt.Printf("Storing %d metrics across %d series\n",
    stats.TotalMetrics, stats.TotalSeries)
```

## Design

All storage backends implement the same `Storage` interface, making them swappable. The interface is intentionally simple - just Write, Query, Delete, and Stats.

The storage layer doesn't handle:
- Downsampling (handled by compaction package)
- Retention policies (handled by server)
- Cardinality limits (handled by ingest)

It just stores and retrieves metrics efficiently.
