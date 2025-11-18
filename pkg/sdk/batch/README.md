# batch

Batching logic for TinyObs SDK - groups metrics before sending to reduce network overhead.

## Why Batch?

Sending 1000 metrics individually = ~5-10 seconds
Sending 1000 metrics in one batch = ~10ms

**1000x more efficient!**

## Configuration

```go
batcher := batch.New(transport, batch.Config{
    MaxBatchSize: 1000,              // Flush when batch reaches this size
    FlushEvery:   5 * time.Second,   // Flush every N seconds
})
```

## Usage

```go
batcher.Start(context.Background())
defer batcher.Stop()

// Add metrics (batched automatically)
batcher.Add(metric)

// Manual flush (optional)
batcher.Flush()
```

## How It Works

Metrics are buffered and flushed when:
- Batch reaches `MaxBatchSize` (1000 metrics)
- `FlushEvery` timer fires (5 seconds)
- `Stop()` is called (graceful shutdown)

## Thread Safety

Multiple goroutines can safely call `Add()` concurrently using `sync.Mutex`.

## Tuning

**Low latency?** → Smaller `FlushEvery` (1s)
**High throughput?** → Larger `MaxBatchSize` (5000)

## Performance

- Batch send: ~10ms for 1000 metrics
- Memory: ~100KB max (pre-allocated buffer)

## Error Handling

Failed sends are logged but not retried. Next batch continues normally. Metrics are fire-and-forget.

## Test Coverage: 0.0%

Needs tests! Contributions welcome.
