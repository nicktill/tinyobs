# batch

Batching logic for the TinyObs SDK. Groups metrics together before sending to reduce network overhead.

## Why Batching?

**Problem:** Sending metrics individually is inefficient.
- Each HTTP request has overhead (~5-10ms per request)
- Sending 1000 metrics individually = 5-10 seconds
- Network can be a bottleneck

**Solution:** Batch metrics together.
- Send 1000 metrics in one request = ~10ms
- ~1000x more efficient
- Less network traffic, less server load

## How It Works

```
Your App                Batcher                 TinyObs Server
   │                       │                          │
   │  counter.Inc()        │                          │
   ├──────────────────────▶│                          │
   │                       │ [buffer]                 │
   │  gauge.Set()          │                          │
   ├──────────────────────▶│                          │
   │                       │ [buffer]                 │
   │  histogram.Observe()  │                          │
   ├──────────────────────▶│                          │
   │                       │ [buffer]                 │
   │                       │                          │
   │     (5 seconds pass or 1000 metrics reached)     │
   │                       │                          │
   │                       │  POST /v1/ingest         │
   │                       ├─────────────────────────▶│
   │                       │  {metrics: [1000 items]} │
   │                       │                          │
   │                       │◀─────────────────────────┤
   │                       │  200 OK                  │
```

## Configuration

```go
batcher := batch.New(transport, batch.Config{
    MaxBatchSize: 1000,         // Flush when batch reaches this size
    FlushEvery:   5 * time.Second,  // Flush every N seconds
})
```

**Tuning:**
- **Low latency needs?** → Smaller `FlushEvery` (e.g., 1s)
- **High throughput needs?** → Larger `MaxBatchSize` (e.g., 5000)
- **Low network bandwidth?** → Larger batches, less frequent flushes

## Usage

The batcher is used internally by the SDK client. You typically don't interact with it directly.

### Manual Usage (Advanced)

```go
import (
    "github.com/nicktill/tinyobs/pkg/sdk/batch"
    "github.com/nicktill/tinyobs/pkg/sdk/transport"
)

// Create transport
trans, _ := transport.NewHTTP("http://localhost:8080/v1/ingest", "")

// Create batcher
batcher := batch.New(trans, batch.Config{
    MaxBatchSize: 1000,
    FlushEvery:   5 * time.Second,
})

// Start background flush loop
batcher.Start(context.Background())
defer batcher.Stop()

// Add metrics
batcher.Add(metrics.Metric{
    Name:  "custom_metric",
    Type:  metrics.CounterType,
    Value: 42,
})

// Manual flush (optional)
batcher.Flush()
```

## Lifecycle

### Start

```go
batcher.Start(ctx)
```

Starts a background goroutine that:
1. Runs a ticker every `FlushEvery` duration
2. Flushes batched metrics to the server
3. Stops when `ctx` is canceled

### Add

```go
batcher.Add(metric)
```

Adds a metric to the current batch. If batch size reaches `MaxBatchSize`, triggers immediate flush in background.

**Thread-safe:** Multiple goroutines can call `Add()` concurrently.

### Flush

```go
batcher.Flush()
```

Immediately sends all buffered metrics. Called automatically:
- Every `FlushEvery` seconds
- When batch reaches `MaxBatchSize`
- On `Stop()`

### Stop

```go
batcher.Stop()
```

1. Cancels the background flush loop
2. Waits for loop to finish
3. Flushes remaining metrics

**Important:** Always call `Stop()` before exiting to avoid losing metrics!

## Implementation Details

### Thread Safety

Uses `sync.Mutex` to protect the metrics buffer:
```go
b.mu.Lock()
b.metrics = append(b.metrics, metric)
b.mu.Unlock()
```

Safe to call from multiple goroutines concurrently.

### Non-Blocking Flush

The `flush()` method sends metrics in a background goroutine:
```go
go b.sendMetrics(metrics)
```

**Why?** Prevents blocking the caller if network is slow.

**Trade-off:** Metrics might be lost if process crashes before send completes.

### Timeout Protection

Each send has a 5-second timeout:
```go
ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
defer cancel()
return b.transport.Send(ctx, metrics)
```

If server is slow or unresponsive, send is aborted to prevent blocking.

## Performance

Benchmarks (1000 metrics per batch):
- **Batched send**: ~10ms
- **Individual sends**: ~10 seconds (1000x slower)

Memory overhead:
- Pre-allocated slice: `make([]Metric, 0, MaxBatchSize)`
- ~100 bytes per metric while buffered
- Max memory: `MaxBatchSize * 100 bytes` (~100KB for 1000)

## Error Handling

Errors during send are logged but don't crash the batcher:
```go
if err := b.sendMetrics(metrics); err != nil {
    log.Printf("Failed to send metrics: %v", err)
}
```

**Behavior on error:**
- Metrics in failed batch are **lost**
- Next flush continues normally
- No retry logic (keeps implementation simple)

**Rationale:** Metrics are fire-and-forget. One dropped batch won't break your app. For critical metrics, use separate storage.

## Common Patterns

### High-Frequency Metrics

If you're generating metrics faster than the flush interval:

```go
// Use larger batch size and shorter flush interval
batcher := batch.New(transport, batch.Config{
    MaxBatchSize: 5000,
    FlushEvery:   1 * time.Second,
})
```

### Low-Latency Requirements

If you need near-real-time metrics:

```go
// Flush more frequently
batcher := batch.New(transport, batch.Config{
    MaxBatchSize: 100,
    FlushEvery:   1 * time.Second,
})
```

### Testing

For tests, use a small flush interval:

```go
batcher := batch.New(mockTransport, batch.Config{
    MaxBatchSize: 10,
    FlushEvery:   100 * time.Millisecond,  // Fast for tests
})
```

## Debugging

Enable verbose logging to see flush activity:

```go
// In your app's main():
log.SetFlags(log.LstdFlags | log.Lmicroseconds)
```

Look for:
- `Flushing N metrics` - Normal flush
- `Failed to send metrics` - Network/server errors

## Comparison to Other Systems

| System | Batching Strategy | Flush Interval |
|--------|------------------|----------------|
| TinyObs | Time or size-based | 5s / 1000 metrics |
| Prometheus | Pull-based (no batching) | N/A (scrape interval) |
| Datadog | Agent-based batching | 10s / 500 metrics |
| StatsD | UDP fire-and-forget | Immediate |

TinyObs is similar to Datadog's approach but simpler.

## Test Coverage

Coverage: **0.0%** (as of v2.2)

**This package needs tests!** Contributions welcome.

Suggested test cases:
- Flush on size limit
- Flush on time interval
- Thread safety (concurrent Add calls)
- Graceful shutdown

## See Also

- `pkg/sdk/transport/` - Where batches are sent
- `pkg/sdk/` - Main SDK client that uses the batcher
- `pkg/ingest/` - Server that receives batched metrics
