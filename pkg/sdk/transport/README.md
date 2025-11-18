# transport

HTTP transport layer for sending metrics to TinyObs server.

## Interface

```go
type Transport interface {
    Send(ctx context.Context, metrics []Metric) error
}
```

## HTTP Transport

```go
trans, _ := transport.NewHTTP(
    "http://localhost:8080/v1/ingest",
    "optional-api-key",  // Empty string for none
)

err := trans.Send(ctx, metrics)
```

## Request Format

```json
{
  "metrics": [{
    "name": "http_requests_total",
    "type": "counter",
    "value": 42,
    "labels": {"service": "api"},
    "timestamp": "2025-11-18T12:34:56Z"
  }]
}
```

Headers:
- `Content-Type: application/json`
- `Authorization: Bearer <api-key>` (if provided)

## Configuration

```go
client: &http.Client{
    Timeout: 10 * time.Second,  // Built-in timeout
    // Uses connection pooling by default
}
```

## Error Handling

Returns error immediately. No retry (metrics are fire-and-forget).

**HTTP Status Errors:**
- `200 OK` - Success
- `400` - Invalid JSON/metrics
- `429` - Cardinality limit
- `500` - Server error

## Performance

- JSON marshal: ~500μs for 1000 metrics
- Request latency: 1-10ms (local network)
- Connection pooling: Reuses TCP connections

## Test Coverage: 0.0%

Needs tests!
