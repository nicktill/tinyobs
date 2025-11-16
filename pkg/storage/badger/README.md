# badger

Production storage using BadgerDB (LSM tree). Persists to disk, survives restarts.

## Why BadgerDB?

- **LSM tree** - Optimized for write-heavy workloads (perfect for metrics)
- **Pure Go** - No CGo dependencies, easy to build
- **Built-in compression** - Snappy compression reduces disk usage significantly
- **Battle-tested** - Used by Dgraph, IPFS, and other production systems

## Usage

```go
import "tinyobs/pkg/storage/badger"

store, err := badger.New(badger.Config{
    Path: "./data/metrics",
})
defer store.Close()

// Writes are batched in transactions
err = store.Write(ctx, metrics)
```

## Storage Format

Keys are sorted by: `[series_hash][timestamp]`

This allows efficient time-range scans and easy compaction.

## Performance

- **Writes**: ~100k metrics/sec on SSD
- **Reads**: Sub-millisecond for recent data
- **Disk usage**: ~1KB per 1000 samples (with compression)

## Limitations

- Single-node only (no replication yet)
- Linear scan for label queries (no inverted index yet)
- Full key scan for deletion (can be slow with billions of metrics)

Good enough for most use cases. Optimizations coming in future versions.
