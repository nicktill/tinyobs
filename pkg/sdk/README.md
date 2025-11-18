# sdk

Client SDK for instrumenting Go applications with TinyObs metrics.

## Quick Start

Add TinyObs to your app in 3 steps:

```go
package main

import (
    "context"
    "net/http"

    "github.com/nicktill/tinyobs/pkg/sdk"
    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
)

func main() {
    // 1. Create TinyObs client
    client, _ := sdk.New(sdk.ClientConfig{
        Service:  "my-app",
        Endpoint: "http://localhost:8080/v1/ingest",
    })

    // 2. Start collecting metrics
    client.Start(context.Background())
    defer client.Stop()

    // 3. Wrap HTTP handlers for automatic metrics
    mux := http.NewServeMux()
    mux.HandleFunc("/", handler)

    wrapped := httpx.Middleware(client)(mux)
    http.ListenAndServe(":3000", wrapped)
}
```

That's it! You now get:
- Request counts by endpoint, method, and status
- Request duration histograms
- Go runtime metrics (memory, goroutines, GC)

## SDK Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Your Application                   │
├─────────────────────────────────────────────────────┤
│                                                     │
│  Your Code          TinyObs SDK                     │
│  ┌─────────────┐    ┌──────────────────┐           │
│  │ handler()   │───▶│ httpx.Middleware │           │
│  │ counter.Inc()│───▶│ metrics.Counter  │           │
│  │ gauge.Set() │───▶│ metrics.Gauge    │           │
│  └─────────────┘    └──────────────────┘           │
│                              │                      │
│                              ▼                      │
│                     ┌──────────────────┐            │
│                     │   Client         │            │
│                     │ - Metric storage │            │
│                     │ - Auto runtime   │            │
│                     └──────────────────┘            │
│                              │                      │
│                              ▼                      │
│                     ┌──────────────────┐            │
│                     │   Batcher        │            │
│                     │ - 5s flush       │            │
│                     │ - 1000/batch     │            │
│                     └──────────────────┘            │
│                              │                      │
│                              ▼                      │
│                     ┌──────────────────┐            │
│                     │   Transport      │            │
│                     │ - HTTP POST      │            │
│                     └──────────────────┘            │
└─────────────────────────────────────────────────────┘
                               │
                               ▼
                    TinyObs Server (localhost:8080)
```

## Core Concepts

### 1. Client

The `Client` is your main entry point. It:
- Manages metric instances (counters, gauges, histograms)
- Automatically adds service labels to all metrics
- Batches and sends metrics every 5 seconds
- Collects Go runtime stats automatically

### 2. Metrics

Three metric types:
- **Counter** - Only goes up (requests, errors, bytes sent)
- **Gauge** - Goes up and down (temperature, queue size, memory)
- **Histogram** - Tracks distributions (latencies, response sizes)

### 3. Batching

Metrics aren't sent immediately. They're batched and flushed:
- Every 5 seconds (configurable via `FlushEvery`)
- When batch reaches 1000 metrics (configurable via `MaxBatchSize`)
- On shutdown (graceful flush)

**Why batch?** Reduces network overhead. Sending 1000 metrics in one request is ~100x more efficient than 1000 individual requests.

### 4. Auto-Instrumentation

The SDK automatically collects:
- **HTTP metrics** (via `httpx.Middleware`)
- **Runtime metrics** (memory, goroutines, GC stats)
- **Service labels** (added to every metric)

## Configuration

```go
client, err := sdk.New(sdk.ClientConfig{
    Service:    "my-app",              // Required: service name
    Endpoint:   "http://localhost:8080/v1/ingest", // TinyObs server
    APIKey:     "optional-api-key",    // Optional: for authentication
    FlushEvery: 5 * time.Second,       // How often to send metrics
})
```

**Defaults:**
- `Endpoint`: `http://localhost:8080/v1/ingest`
- `FlushEvery`: `5s`
- `MaxBatchSize`: `1000 metrics`

## Creating Custom Metrics

### Counter - For things that only increase

```go
// Create counter
requestCounter := client.Counter("http_requests_total")

// Increment by 1
requestCounter.Inc("endpoint", "/api/users", "status", "200")

// Add custom amount
requestCounter.Add(5, "endpoint", "/api/batch", "status", "200")
```

**Use cases:**
- Total requests
- Errors
- Bytes transferred
- Items processed

### Gauge - For values that go up and down

```go
// Create gauge
queueSize := client.Gauge("job_queue_size")

// Set absolute value
queueSize.Set(42)

// Increment/decrement
queueSize.Inc()  // Add 1
queueSize.Dec()  // Subtract 1
```

**Use cases:**
- Queue size
- Temperature
- Current memory usage
- Active connections

### Histogram - For distributions

```go
// Create histogram
latency := client.Histogram("request_duration_seconds")

// Observe a value
start := time.Now()
// ... do work ...
latency.Observe(time.Since(start).Seconds(), "endpoint", "/api/users")
```

**Use cases:**
- Request latencies
- Response sizes
- Database query times
- Processing durations

## Labels

Labels add dimensions to metrics:

```go
counter.Inc("endpoint", "/users", "method", "GET", "status", "200")
```

This creates a unique series: `http_requests_total{endpoint="/users", method="GET", status="200"}`

**Best practices:**
- ✅ Use low-cardinality labels (status codes, endpoints, methods)
- ❌ Avoid high-cardinality labels (user IDs, UUIDs, timestamps)
- ✅ Keep label count reasonable (3-5 labels per metric)
- ❌ Don't use labels for values that change frequently

**Why?** High cardinality = more storage, slower queries, potential cardinality limit errors.

## Lifecycle Management

### Start the Client

```go
ctx := context.Background()
client.Start(ctx)
```

This:
1. Starts the batcher (begins 5s flush loop)
2. Starts runtime metrics collection (every 10s)

### Stop the Client

```go
client.Stop()
```

This:
1. Stops the batcher
2. Flushes remaining metrics
3. Waits for send to complete

**Important:** Always call `Stop()` before exiting to avoid losing metrics!

```go
defer client.Stop()  // ✅ Good - ensures flush on panic
```

## HTTP Middleware

Wrap your HTTP handlers to get automatic metrics:

```go
mux := http.NewServeMux()
mux.HandleFunc("/users", usersHandler)
mux.HandleFunc("/orders", ordersHandler)

// Wrap with TinyObs middleware
handler := httpx.Middleware(client)(mux)
http.ListenAndServe(":8080", handler)
```

This automatically tracks:
- `http_requests_total{method, path, status}` - Request count
- `http_request_duration_seconds{method, path, status}` - Latency histogram

## Runtime Metrics

The SDK automatically collects Go runtime stats every 10 seconds:

| Metric | Type | Description |
|--------|------|-------------|
| `go_memstats_heap_alloc_bytes` | Gauge | Current heap allocation |
| `go_memstats_heap_sys_bytes` | Gauge | Total heap memory |
| `go_memstats_heap_objects` | Gauge | Number of allocated objects |
| `go_goroutines` | Gauge | Current goroutine count |
| `go_gc_cycles_total` | Counter | Total GC cycles |
| `go_memstats_gc_duration_seconds` | Counter | Cumulative GC pause time |

**Use cases:**
- Memory leak detection
- Goroutine leak detection
- GC pressure monitoring

## Examples

### Basic Counter

```go
client, _ := sdk.New(sdk.ClientConfig{Service: "api"})
client.Start(context.Background())
defer client.Stop()

counter := client.Counter("api_calls_total")
counter.Inc("endpoint", "/users")
```

### Track Active Connections

```go
connections := client.Gauge("active_connections")

func handleConnection(conn net.Conn) {
    connections.Inc()
    defer connections.Dec()

    // Handle connection...
}
```

### Measure Function Duration

```go
latency := client.Histogram("function_duration_seconds")

func expensiveOperation() {
    start := time.Now()
    defer func() {
        latency.Observe(time.Since(start).Seconds(), "operation", "expensive")
    }()

    // Do work...
}
```

## Thread Safety

All SDK components are thread-safe:
- ✅ Safe to call `counter.Inc()` from multiple goroutines
- ✅ Safe to create metrics from multiple goroutines
- ✅ Internal synchronization with `sync.Mutex`

## Performance

- **Metric creation**: ~500ns (cached after first call)
- **Counter.Inc()**: ~100ns (mutex + map lookup)
- **Batch send**: ~10ms for 1000 metrics
- **Memory overhead**: ~100 bytes per unique series

For a typical web server with 50 endpoints:
- Memory: ~5KB for metric storage
- CPU: <0.1% overhead
- Network: ~1 request every 5s

## Error Handling

The SDK never panics. Errors are handled gracefully:
- Failed metric sends are logged but don't block your app
- Invalid configurations return errors from `New()`
- Network failures retry on next flush interval

## Testing Your Instrumentation

Use in-memory mode for testing:

```go
func TestHandler(t *testing.T) {
    client, _ := sdk.New(sdk.ClientConfig{
        Service:  "test-app",
        Endpoint: "http://localhost:9999/v1/ingest",  // Fake endpoint
    })
    client.Start(context.Background())
    defer client.Stop()

    // Test your handler...
}
```

Or mock the client interface for unit tests.

## Subpackages

- **`batch/`** - Batching logic (groups metrics before sending)
- **`httpx/`** - HTTP middleware for auto-instrumentation
- **`metrics/`** - Counter, Gauge, Histogram implementations
- **`runtime/`** - Go runtime metrics collector
- **`transport/`** - HTTP transport layer for sending metrics

See individual README files in each subpackage for details.

## Common Patterns

### Service Template

```go
type Service struct {
    client   *sdk.Client
    requests *metrics.Counter
    errors   *metrics.Counter
    duration *metrics.Histogram
}

func NewService() *Service {
    client, _ := sdk.New(sdk.ClientConfig{Service: "my-service"})

    return &Service{
        client:   client,
        requests: client.Counter("requests_total"),
        errors:   client.Counter("errors_total"),
        duration: client.Histogram("duration_seconds"),
    }
}

func (s *Service) Start() {
    s.client.Start(context.Background())
}

func (s *Service) Stop() {
    s.client.Stop()
}
```

### Graceful Shutdown

```go
func main() {
    client, _ := sdk.New(sdk.ClientConfig{Service: "api"})
    client.Start(context.Background())

    // Catch interrupt signal
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)

    <-c
    log.Println("Shutting down...")
    client.Stop()  // Flush remaining metrics
}
```

## Test Coverage

Coverage: **75.8%** (as of v2.2)

Core client logic is well-tested. Subpackages need more tests.

## See Also

- `pkg/ingest/` - Server that receives metrics
- `pkg/sdk/batch/` - Batching internals
- `pkg/sdk/httpx/` - HTTP middleware
- `pkg/sdk/metrics/` - Metric type implementations
