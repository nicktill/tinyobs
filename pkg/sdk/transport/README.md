# transport

HTTP transport layer for sending metrics to TinyObs server. Handles network communication, serialization, and error handling.

## Purpose

The transport package abstracts how metrics are sent. Currently supports HTTP, but designed to support other transports (gRPC, UDP, etc.) in the future.

## Interface

```go
type Transport interface {
    Send(ctx context.Context, metrics []Metric) error
}
```

**Simple contract:** Give me metrics, I'll send them. You don't care how.

## HTTP Transport

The default (and currently only) transport implementation.

### Creation

```go
import "github.com/nicktill/tinyobs/pkg/sdk/transport"

trans, err := transport.NewHTTP(
    "http://localhost:8080/v1/ingest",  // Endpoint
    "optional-api-key",                  // API key (empty string if none)
)
```

### Configuration

```go
type HTTPTransport struct {
    endpoint string          // Where to send metrics
    apiKey   string          // Optional authentication
    client   *http.Client    // Reusable HTTP client
}
```

**HTTP Client settings:**
- Timeout: 10 seconds
- Reuses TCP connections (HTTP keep-alive)
- No retry logic (fail fast)

## How It Works

```
Batch              Transport                TinyObs Server
  │                    │                          │
  │  Send(metrics)     │                          │
  ├───────────────────▶│                          │
  │                    │                          │
  │                    │ 1. Marshal to JSON       │
  │                    │                          │
  │                    │ 2. Create POST request   │
  │                    │                          │
  │                    │ 3. Add headers           │
  │                    │  - Content-Type: application/json
  │                    │  - Authorization: Bearer <key>
  │                    │                          │
  │                    │ 4. Send HTTP POST        │
  │                    ├─────────────────────────▶│
  │                    │  POST /v1/ingest         │
  │                    │  {"metrics": [...]}      │
  │                    │                          │
  │                    │◀─────────────────────────┤
  │                    │  200 OK                  │
  │                    │                          │
  │  nil (success)     │                          │
  │◀───────────────────┤                          │
```

## Usage

The transport is used internally by the batcher. You typically don't interact with it directly.

### Direct Usage (Advanced)

```go
import (
    "context"
    "github.com/nicktill/tinyobs/pkg/sdk/transport"
    "github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// Create transport
trans, _ := transport.NewHTTP("http://localhost:8080/v1/ingest", "")

// Send metrics
err := trans.Send(context.Background(), []metrics.Metric{
    {
        Name:  "custom_metric",
        Type:  metrics.CounterType,
        Value: 42,
    },
})

if err != nil {
    log.Printf("Failed to send: %v", err)
}
```

## Request Format

### Payload

```json
{
  "metrics": [
    {
      "name": "http_requests_total",
      "type": "counter",
      "value": 1547,
      "labels": {
        "service": "api",
        "endpoint": "/users",
        "status": "200"
      },
      "timestamp": "2025-11-18T12:34:56Z"
    },
    {
      "name": "request_duration_seconds",
      "type": "histogram",
      "value": 0.034,
      "labels": {
        "service": "api",
        "endpoint": "/users"
      },
      "timestamp": "2025-11-18T12:34:56Z"
    }
  ]
}
```

### Headers

```
POST /v1/ingest HTTP/1.1
Host: localhost:8080
Content-Type: application/json
Authorization: Bearer <api-key>  (if provided)
Content-Length: 456
```

## Authentication

### No Authentication (Default)

```go
trans, _ := transport.NewHTTP("http://localhost:8080/v1/ingest", "")
// No Authorization header sent
```

### With API Key

```go
trans, _ := transport.NewHTTP(
    "http://localhost:8080/v1/ingest",
    "my-secret-api-key",
)
// Sends: Authorization: Bearer my-secret-api-key
```

**Note:** TinyObs server doesn't currently validate API keys. This is for future use.

## Error Handling

### Network Errors

```go
err := trans.Send(ctx, metrics)
if err != nil {
    // Could be:
    // - DNS lookup failed
    // - Connection refused
    // - Timeout
    // - etc.
}
```

**Behavior:** Returns error immediately. No retry.

**Why no retry?** Metrics are fire-and-forget. If one batch fails, it's not worth blocking to retry. Next batch will be sent in 5 seconds anyway.

### HTTP Status Errors

```go
// Server returns 4xx or 5xx
err := trans.Send(ctx, metrics)
// err: "request failed with status 500"
```

**Status code meanings:**
- `200 OK` - Success
- `400 Bad Request` - Invalid JSON or metric format
- `429 Too Many Requests` - Cardinality limit exceeded
- `500 Internal Server Error` - Server error

### Context Cancellation

```go
ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
defer cancel()

err := trans.Send(ctx, metrics)
// If timeout: err = "context deadline exceeded"
```

**Built-in timeout:** 10 seconds (via `http.Client.Timeout`)

**Additional timeout:** Via context (e.g., 5s from batcher)

**Effective timeout:** Minimum of the two

## Performance

### Serialization

JSON marshaling is fast:
- 1000 metrics → ~50KB JSON
- Marshal time: ~500μs
- Negligible overhead

### Network

Typical request:
- Size: 10-100 KB (1000 metrics)
- Latency: 1-10ms (local network)
- Throughput: 100k metrics/sec (limited by network, not code)

### Connection Pooling

The HTTP client reuses connections:
```go
client: &http.Client{
    Timeout: 10 * time.Second,
    // Default transport uses connection pooling
}
```

**Benefits:**
- No TCP handshake on every request
- Faster sends (reuse existing connection)
- Lower latency

## Debugging

### Enable HTTP Tracing

```go
import "net/http/httptrace"

trace := &httptrace.ClientTrace{
    GotConn: func(info httptrace.GotConnInfo) {
        log.Printf("Connection reused: %v", info.Reused)
    },
    DNSStart: func(info httptrace.DNSStartInfo) {
        log.Printf("DNS lookup: %s", info.Host)
    },
}

ctx = httptrace.WithClientTrace(ctx, trace)
trans.Send(ctx, metrics)
```

### Log Requests

```go
// Wrap transport to log all requests
type loggingTransport struct {
    inner transport.Transport
}

func (t *loggingTransport) Send(ctx context.Context, metrics []Metric) error {
    log.Printf("Sending %d metrics", len(metrics))
    err := t.inner.Send(ctx, metrics)
    if err != nil {
        log.Printf("Send failed: %v", err)
    } else {
        log.Printf("Send successful")
    }
    return err
}
```

## Future Transports

The interface design allows pluggable transports:

### gRPC Transport (Future)

```go
type GRPCTransport struct {
    client grpc.ClientConn
}

func (t *GRPCTransport) Send(ctx context.Context, metrics []Metric) error {
    // Send via gRPC
}
```

**Benefits:**
- Faster serialization (Protocol Buffers)
- HTTP/2 multiplexing
- Bi-directional streaming

### UDP Transport (Future)

```go
type UDPTransport struct {
    conn net.Conn
}

func (t *UDPTransport) Send(ctx context.Context, metrics []Metric) error {
    // Send via UDP (fire and forget)
}
```

**Benefits:**
- Lower latency (no TCP handshake)
- No backpressure
- Good for high-frequency metrics

**Trade-offs:**
- No delivery guarantee
- Packet size limits
- Not reliable over internet

## Testing

### Mock Transport

```go
type mockTransport struct {
    metrics [][]Metric
    err     error
}

func (m *mockTransport) Send(ctx context.Context, metrics []Metric) error {
    m.metrics = append(m.metrics, metrics)
    return m.err
}

// Use in tests
func TestBatcher(t *testing.T) {
    mock := &mockTransport{}
    batcher := batch.New(mock, batch.Config{...})

    // Test batching logic...

    if len(mock.metrics) != 1 {
        t.Errorf("Expected 1 batch, got %d", len(mock.metrics))
    }
}
```

### Test Server

```go
// Create test server
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify request
    if r.Method != "POST" {
        t.Errorf("Expected POST, got %s", r.Method)
    }

    // Respond
    w.WriteHeader(http.StatusOK)
}))
defer server.Close()

// Create transport pointing to test server
trans, _ := transport.NewHTTP(server.URL, "")

// Test sending
err := trans.Send(context.Background(), metrics)
```

## Common Issues

### Connection Refused

```
Error: dial tcp 127.0.0.1:8080: connect: connection refused
```

**Cause:** TinyObs server not running

**Fix:** Start server: `go run cmd/server/main.go`

### Timeout

```
Error: context deadline exceeded
```

**Cause:** Server too slow or network issue

**Fix:**
- Check server load
- Increase timeout
- Check network connectivity

### 400 Bad Request

```
Error: request failed with status 400
```

**Cause:** Invalid metric format or JSON

**Fix:** Check metric struct matches expected format

### 429 Too Many Requests

```
Error: request failed with status 429
```

**Cause:** Cardinality limit exceeded

**Fix:**
- Reduce label cardinality
- Check for high-cardinality labels (user IDs, UUIDs)
- Increase server cardinality limits

## Comparison to Other Systems

| System | Transport | Protocol |
|--------|-----------|----------|
| TinyObs | HTTP POST | JSON over HTTP/1.1 |
| Prometheus | Pull (scrape) | Text format over HTTP |
| Datadog | Agent (HTTP) | JSON over HTTP |
| StatsD | UDP | Custom text protocol |
| OpenTelemetry | gRPC | Protocol Buffers |

TinyObs uses HTTP POST because:
- ✅ Simple, well-understood
- ✅ Works everywhere (firewalls, proxies)
- ✅ Easy to debug (curl, browser)
- ✅ No special dependencies

## Performance Tuning

### Reduce Payload Size

If network is bottleneck:
```go
// Send smaller batches more frequently
batcher := batch.New(trans, batch.Config{
    MaxBatchSize: 100,    // Smaller batches
    FlushEvery:   1 * time.Second,
})
```

### Reduce Network Calls

If latency is bottleneck:
```go
// Send larger batches less frequently
batcher := batch.New(trans, batch.Config{
    MaxBatchSize: 5000,   // Larger batches
    FlushEvery:   10 * time.Second,
})
```

### Custom HTTP Client

```go
// Create custom client with tuned settings
customClient := &http.Client{
    Timeout: 30 * time.Second,  // Longer timeout
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 10,
        IdleConnTimeout:     90 * time.Second,
    },
}

// Inject into transport (requires code change)
trans := &HTTPTransport{
    endpoint: endpoint,
    apiKey:   apiKey,
    client:   customClient,  // Use custom client
}
```

## Test Coverage

Coverage: **0.0%** (as of v2.2)

**This package needs tests!** Contributions welcome.

Suggested test cases:
- Successful send
- Network error handling
- HTTP error status codes
- Authorization header
- Context cancellation
- JSON serialization

## See Also

- `pkg/sdk/batch/` - Batches metrics before sending
- `pkg/ingest/` - Server that receives transported metrics
- `net/http` - Go standard library HTTP client
