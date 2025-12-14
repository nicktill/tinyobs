# sdk

Client SDK for instrumenting Go applications with TinyObs.

## Quick Start

```go
import (
    "github.com/nicktill/tinyobs/pkg/sdk"
    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
)

func main() {
    // Create client
    client, _ := sdk.New(sdk.ClientConfig{
        Service:  "my-app",
        Endpoint: "http://localhost:8080/v1/ingest",
    })

    client.Start(context.Background())
    defer client.Stop()

    // Wrap HTTP handlers for auto-metrics
    mux := http.NewServeMux()
    handler := httpx.Middleware(client)(mux)
    http.ListenAndServe(":3000", handler)
}
```

You get:
- Request counts/durations by endpoint
- Go runtime metrics (memory, goroutines, GC)

## Metric Types

### Counter - Only Goes Up
```go
counter := client.Counter("requests_total")
counter.Inc("endpoint", "/users")
counter.Add(5, "endpoint", "/batch")
```

Use for: requests, errors, bytes transferred

### Gauge - Goes Up and Down
```go
gauge := client.Gauge("queue_size")
gauge.Set(42)
gauge.Inc() / gauge.Dec()
```

Use for: queue size, memory usage, active connections

### Histogram - Distributions
```go
histogram := client.Histogram("request_duration_seconds")
histogram.Observe(0.034, "endpoint", "/users")
```

Use for: latencies, response sizes, durations

## Configuration

```go
client, _ := sdk.New(sdk.ClientConfig{
    Service:    "my-app",              // Required
    Endpoint:   "http://localhost:8080/v1/ingest",
    FlushEvery: 5 * time.Second,       // Batch frequency
})
```

Defaults: 5s flush, 1000 metrics/batch

## Labels

```go
counter.Inc("endpoint", "/users", "method", "GET", "status", "200")
```

Best practices:
- ✅ Low cardinality (status codes, endpoints)
- ❌ High cardinality (user IDs, timestamps)

## Lifecycle

```go
client.Start(ctx)   // Starts batching + runtime collection
client.Stop()       // Flushes remaining metrics
```

Always `defer client.Stop()` to avoid losing metrics!

## Auto-Instrumentation

```go
mux := http.NewServeMux()
wrapped := httpx.Middleware(client)(mux)
```

Auto-tracks:
- `http_requests_total{method, path, status}`
- `http_request_duration_seconds{method, path, status}`

## Runtime Metrics

Automatically collected every 15s:
- `go_goroutines` - Number of goroutines
- `go_cpu_count` - Number of CPU cores
- `go_memory_heap_bytes` - Heap memory usage
- `go_memory_stack_bytes` - Stack memory usage
- `go_memory_sys_bytes` - Total memory from OS
- `go_gc_count` - Total GC runs
- `go_gc_pause_seconds` - GC pause duration

## Performance

- Metric creation: ~500ns
- Counter.Inc(): ~100ns
- Batch send: ~10ms for 1000 metrics
- Memory: ~100 bytes per unique series

## Subpackages

- `batch/` - Batching logic
- `httpx/` - HTTP middleware
- `metrics/` - Counter/Gauge/Histogram
- `runtime/` - Runtime metrics collector
- `transport/` - HTTP transport layer

## Test Coverage: 75.8%

Core logic well-tested. Sub packages need tests.
