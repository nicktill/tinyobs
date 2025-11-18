# ingest

Server-side metric ingestion and query API. This is the core of the TinyObs server.

## Purpose

The ingest package handles:
- **REST API endpoints** for receiving and querying metrics
- **WebSocket hub** for real-time metric streaming to dashboards
- **Cardinality protection** to prevent label explosion
- **Prometheus /metrics endpoint** for Grafana compatibility

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                 Ingest Layer                        │
├─────────────────────────────────────────────────────┤
│                                                     │
│  REST API              WebSocket Hub               │
│  ┌─────────────┐      ┌──────────────┐            │
│  │ /v1/ingest  │──┐   │ Real-time    │            │
│  │ /v1/query   │  │   │ Broadcasting │            │
│  │ /v1/stats   │  │   │              │            │
│  └─────────────┘  │   └──────────────┘            │
│                   │                                │
│  Cardinality      │   Prometheus Export            │
│  ┌─────────────┐  │   ┌──────────────┐            │
│  │ Tracker     │◄─┤   │ /metrics     │            │
│  │ Limits      │  │   │ (Grafana)    │            │
│  └─────────────┘  │   └──────────────┘            │
│                   │                                │
│                   ▼                                │
│            ┌──────────────┐                        │
│            │   Storage    │                        │
│            └──────────────┘                        │
└─────────────────────────────────────────────────────┘
```

## Usage

### Create Handler

```go
import (
    "github.com/nicktill/tinyobs/pkg/ingest"
    "github.com/nicktill/tinyobs/pkg/storage/badger"
)

// Create storage backend
store, _ := badger.New(badger.Config{Path: "./data"})

// Create ingest handler
handler := ingest.NewHandler(store)
```

### REST API Endpoints

#### POST /v1/ingest
Ingest metrics from SDK clients.

```bash
curl -X POST http://localhost:8080/v1/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "metrics": [
      {
        "name": "http_requests_total",
        "type": "counter",
        "value": 42,
        "labels": {"service": "api", "endpoint": "/users"},
        "timestamp": "2025-11-18T00:00:00Z"
      }
    ]
  }'
```

**Response:**
```json
{
  "status": "success",
  "count": 1
}
```

**Limits:**
- Max 10,000 metrics per request (configurable via `MaxMetricsPerRequest`)
- Max 1,000 unique label combinations per metric (cardinality limit)

#### GET /v1/query
Query metrics by name and time range.

```bash
curl "http://localhost:8080/v1/query?metric=http_requests_total&start=2025-11-18T00:00:00Z"
```

#### GET /v1/stats
Get storage statistics (total metrics, series count, storage size).

```bash
curl http://localhost:8080/v1/stats
```

#### GET /v1/cardinality
Get cardinality usage stats (useful for detecting label explosions).

```bash
curl http://localhost:8080/v1/cardinality
```

#### GET /metrics
Prometheus-compatible metrics export (works with Grafana).

```bash
curl http://localhost:8080/metrics
```

### WebSocket Real-Time Updates

Connect to `/ws` for live metric updates:

```javascript
const ws = new WebSocket('ws://localhost:8080/ws');

ws.onmessage = (event) => {
  const update = JSON.parse(event.data);
  console.log('New metrics:', update);
};
```

**Features:**
- Broadcasts new metrics to all connected clients
- Ping/pong keepalive (30s interval)
- Automatic reconnection on disconnect
- Graceful cleanup on server shutdown

## Cardinality Protection

TinyObs prevents "cardinality explosions" where too many unique label combinations cause storage/memory issues.

**How it works:**
1. Tracks unique label combinations per metric
2. Rejects new series when limit exceeded (default: 1,000)
3. Protects against accidental high-cardinality labels (e.g., user IDs, UUIDs)

**Example:**
```
✅ Good:  service="api", endpoint="/users", status="200"  (low cardinality)
❌ Bad:   service="api", user_id="12345678"              (high cardinality)
```

## Configuration

Default limits (defined in `limits.go`):
```go
MaxMetricsPerRequest  = 10000  // Max metrics per /v1/ingest request
MaxSeriesPerMetric    = 1000   // Max unique label combos per metric
ingestTimeout         = 10s    // Write timeout
queryTimeout          = 30s    // Query timeout
```

## Error Handling

The handler returns appropriate HTTP status codes:
- `200 OK` - Success
- `400 Bad Request` - Invalid JSON or metric format
- `405 Method Not Allowed` - Wrong HTTP method
- `429 Too Many Requests` - Cardinality limit exceeded
- `500 Internal Server Error` - Storage failure

## Performance

Typical performance on modern hardware:
- **Ingest**: ~50k metrics/sec (batched requests)
- **Query**: ~10ms for 24h time range (with auto-downsampling)
- **WebSocket**: Broadcasts to 100+ clients with <1ms latency
- **Cardinality check**: O(1) per metric (hash map lookup)

## Why This Design?

**REST API** - Simple, well-understood, works everywhere
- No custom protocols to learn
- Works with curl, Postman, any HTTP client
- Easy to debug with browser dev tools

**WebSocket for real-time** - Better than polling
- Reduces server load (no repeated polling)
- Lower latency (<100ms vs 30s polling)
- Automatically handles reconnection

**Cardinality limits** - Learned from the 53-day bug
- Prevents unbounded memory growth
- Catches mistakes early (wrong label usage)
- Makes metrics costs predictable

## Testing

Test coverage: **13.5%** (as of v2.2)

**Tested:**
- Basic ingest validation
- Cardinality tracking

**Not tested yet:**
- WebSocket hub functionality
- Dashboard API endpoints
- Error handling edge cases

This is an area for improvement - contributions welcome!

## See Also

- `pkg/storage/` - Storage backends (where metrics go)
- `pkg/sdk/` - Client SDK (how apps send metrics)
- `web/dashboard.html` - Dashboard that consumes WebSocket updates
