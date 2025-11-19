# Distributed Tracing in TinyObs

TinyObs includes built-in distributed tracing that tracks requests as they flow through multiple services. This is similar to what tools like Jaeger, Zipkin, and Datadog APM provide, but integrated directly into TinyObs.

## What is Distributed Tracing?

Distributed tracing follows a single request (like an API call) as it travels through your system:

1. **User makes request** → API service receives it
2. **API service** → Calls database service
3. **Database service** → Queries data
4. **Response flows back** through the chain

Each step is recorded as a "span", and all spans for one request form a "trace". You can see exactly:
- Which services were involved
- How long each operation took
- Where errors occurred
- The complete request flow

## Key Concepts

### Trace
A complete journey of a single request through your system. Identified by a unique Trace ID.

### Span
A single operation within a trace (like "query database" or "call API"). Each span has:
- **Span ID**: Unique identifier
- **Parent ID**: Links to the previous operation (creates the tree structure)
- **Start/End time**: When the operation began and finished
- **Service**: Which service created this span
- **Operation**: What was being done (e.g., "GET /api/users")
- **Tags**: Metadata (HTTP status, error messages, etc.)

### Trace Context
Information that flows between services to connect spans:
- Trace ID (same for entire request)
- Span ID (current operation)
- Parent Span ID (previous operation)
- Sampling flag (whether to record this trace)

## Quick Start

### 1. Basic Usage

```go
package main

import (
    "context"
    "github.com/nicktill/tinyobs/pkg/tracing"
)

func main() {
    // Create storage and tracer
    storage := tracing.NewStorage()
    tracer := tracing.NewTracer("my-service", storage)

    // Start a span
    ctx, span := tracer.StartSpan(context.Background(), "process_request", tracing.SpanKindServer)

    // Add metadata
    tracer.SetTag(span, "user_id", "12345")

    // Do work...

    // Finish span
    tracer.FinishSpan(ctx, span)
}
```

### 2. HTTP Middleware (Automatic Tracing)

The easiest way to add tracing is using the HTTP middleware:

```go
package main

import (
    "net/http"
    "github.com/nicktill/tinyobs/pkg/tracing"
)

func main() {
    storage := tracing.NewStorage()
    tracer := tracing.NewTracer("api-service", storage)

    mux := http.NewServeMux()
    mux.HandleFunc("/api/users", handleUsers)

    // Wrap with tracing middleware - automatically creates spans!
    handler := tracing.HTTPMiddleware(tracer)(mux)

    http.ListenAndServe(":8080", handler)
}
```

This automatically:
- Creates a span for each HTTP request
- Extracts trace context from incoming headers
- Records HTTP metadata (method, path, status code)
- Propagates trace context to downstream services

### 3. Child Spans (Nested Operations)

```go
func handleUsers(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context() // Contains trace context from middleware

    // Create a child span for database query
    ctx, dbSpan := tracer.StartSpan(ctx, "db.query.users", tracing.SpanKindInternal)
    tracer.SetTag(dbSpan, "db.query", "SELECT * FROM users")

    users, err := database.QueryUsers(ctx)

    if err != nil {
        tracer.FinishSpanWithError(ctx, dbSpan, err)
        http.Error(w, "Database error", 500)
        return
    }

    tracer.FinishSpan(ctx, dbSpan)

    // Response...
}
```

### 4. Cross-Service Tracing

When calling another service, propagate the trace context via HTTP headers:

```go
func callAnotherService(ctx context.Context) error {
    req, _ := http.NewRequest("GET", "http://other-service/api", nil)

    // Inject trace context into headers
    if tc, ok := tracing.GetTraceContext(ctx); ok {
        tracing.InjectHeaders(req, tc)
    }

    client := &http.Client{}
    resp, err := client.Do(req)
    // ...
}
```

Or use the HTTP client middleware for automatic injection:

```go
tracer := tracing.NewTracer("my-service", storage)

client := &http.Client{
    Transport: tracing.HTTPClientMiddleware(tracer, http.DefaultTransport),
}

// All requests automatically include trace context!
resp, err := client.Get("http://other-service/api")
```

## Span Types

TinyObs supports three span kinds:

### SpanKindServer
For incoming requests (HTTP handlers, RPC servers):
```go
ctx, span := tracer.StartSpan(ctx, "GET /api/users", tracing.SpanKindServer)
```

### SpanKindClient
For outgoing requests (HTTP calls, RPC clients):
```go
ctx, span := tracer.StartSpan(ctx, "http.get.user-service", tracing.SpanKindClient)
```

### SpanKindInternal
For internal operations (database queries, business logic):
```go
ctx, span := tracer.StartSpan(ctx, "calculate_taxes", tracing.SpanKindInternal)
```

## Error Handling

Mark spans as errored:

```go
ctx, span := tracer.StartSpan(ctx, "process_payment", tracing.SpanKindInternal)

result, err := processPayment()
if err != nil {
    // Automatically marks span as error and records error message
    tracer.FinishSpanWithError(ctx, span, err)
    return err
}

tracer.FinishSpan(ctx, span)
```

## Visualization

View traces in the TinyObs UI at `http://localhost:8080/traces.html`.

The waterfall chart shows:
- **Each span as a horizontal bar** (length = duration)
- **Span hierarchy** (parent-child relationships)
- **Service colors** (different services in different colors)
- **Error indicators** (red bars for failed operations)
- **Timing information** (exact duration in ms/μs)

### UI Features:
- Click any trace to see detailed waterfall
- View all spans with timing information
- See HTTP metadata (status codes, URLs)
- Identify errors at a glance
- Analyze performance bottlenecks

## API Endpoints

### GET /v1/traces/recent?limit=50
Get the most recent traces:
```bash
curl http://localhost:8080/v1/traces/recent?limit=50
```

### GET /v1/traces?start=<time>&end=<time>&limit=100
Query traces by time range:
```bash
curl "http://localhost:8080/v1/traces?start=2025-11-19T00:00:00Z&end=2025-11-19T23:59:59Z&limit=100"
```

### GET /v1/trace?trace_id=<id>
Get a specific trace by ID:
```bash
curl "http://localhost:8080/v1/trace?trace_id=abc123def456..."
```

### GET /v1/traces/stats
Get tracing statistics:
```bash
curl http://localhost:8080/v1/traces/stats
```

### POST /v1/traces/ingest
Manually ingest a span (for external tracers):
```bash
curl -X POST http://localhost:8080/v1/traces/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "trace_id": "abc123",
    "span_id": "xyz789",
    "service": "my-service",
    "operation": "test",
    "start_time": "2025-11-19T10:00:00Z",
    "end_time": "2025-11-19T10:00:01Z",
    "kind": "server",
    "status": "ok"
  }'
```

## Configuration

### Storage Limits

The default configuration stores:
- **Max traces**: 10,000 traces
- **Max age**: 24 hours
- **Memory**: ~50-100 MB for typical workloads

Traces are automatically cleaned up when:
- They exceed the 24-hour retention period
- Total trace count exceeds 10,000 (oldest are removed)

### W3C Trace Context

TinyObs implements the [W3C Trace Context](https://www.w3.org/TR/trace-context/) standard:

**HTTP Header Format:**
```
traceparent: 00-{trace-id}-{span-id}-{flags}
```

Example:
```
traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01
```

This means TinyObs tracing is compatible with:
- OpenTelemetry
- Jaeger
- Zipkin
- Cloud providers (AWS X-Ray, Google Cloud Trace)

## Performance Tips

1. **Sampling**: For high-traffic services, consider sampling (e.g., trace 10% of requests)
   - Currently TinyObs samples 100% of traces
   - Production systems often sample 1-10%

2. **Tag cardinality**: Avoid high-cardinality tags (user IDs, session IDs)
   - Good: `http.method`, `http.status_code`
   - Bad: `user_id`, `request_id` (creates too many unique combinations)

3. **Span granularity**: Don't create spans for every tiny operation
   - Good: Database query, HTTP call, major business logic
   - Bad: Individual variable assignments, simple calculations

## Comparison to Other Tools

| Feature | TinyObs | Jaeger | Datadog APM | Zipkin |
|---------|---------|--------|-------------|--------|
| Installation | Built-in | Requires infrastructure | SaaS | Requires infrastructure |
| Storage | In-memory (24h) | Cassandra/Elasticsearch | Cloud | Cassandra/Elasticsearch |
| Visualization | Waterfall chart | Advanced UI | Advanced UI | Timeline view |
| Cost | Free | Free (self-hosted) | $$$$ | Free (self-hosted) |
| Best for | Local dev, learning | Production, scale | Production SaaS | Production |

## Example: Multi-Service Architecture

See `cmd/example/tracing_example.go` for a complete example showing:
- API service receiving requests
- Database service handling queries
- Cache service for lookups
- Trace context propagation between services
- Error handling and recording
- HTTP metadata capture

Run the example:
```bash
# Terminal 1: Start TinyObs server
go run cmd/server/main.go

# Terminal 2: Run tracing example
go run cmd/example/main.go

# Terminal 3: View traces
open http://localhost:8080/traces.html
```

## Troubleshooting

### No traces showing up
1. Check that TinyObs server is running
2. Verify spans are being finished (call `FinishSpan`)
3. Check browser console for errors
4. Verify trace context is being propagated

### Trace context not propagating
1. Ensure you're using the HTTP middleware
2. Check that headers are being set: `traceparent`
3. Verify context is passed through function calls
4. Use `tracing.InjectHeaders` for manual propagation

### Performance issues
1. Reduce sampling rate for high-traffic services
2. Limit tag cardinality
3. Avoid creating too many spans (keep it coarse-grained)
4. Check storage cleanup is running (should auto-cleanup every 5 minutes)

## Future Enhancements

Potential improvements for future versions:
- [ ] Persistent storage (BadgerDB integration)
- [ ] Configurable sampling rates
- [ ] Trace search and filtering
- [ ] Service dependency graphs
- [ ] Performance analytics (p50, p95, p99)
- [ ] Export to Jaeger/Zipkin format
- [ ] Distributed trace aggregation across multiple TinyObs instances
