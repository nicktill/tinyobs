# ingest

Server-side metric ingestion and query API.

## What It Does

- REST API endpoints (`/v1/ingest`, `/v1/query`, `/v1/stats`)
- WebSocket hub for real-time metric streaming
- Cardinality protection (prevents label explosion)
- Prometheus `/metrics` endpoint (Grafana compatible)

## Quick Start

```go
store, _ := badger.New(badger.Config{Path: "./data"})
handler := ingest.NewHandler(store)
```

## API Endpoints

**POST /v1/ingest** - Ingest metrics
```bash
curl -X POST http://localhost:8080/v1/ingest -d '{
  "metrics": [{
    "name": "http_requests_total",
    "type": "counter",
    "value": 42,
    "labels": {"service": "api"},
    "timestamp": "2025-11-18T00:00:00Z"
  }]
}'
```

Limits: 10k metrics/request, 1k unique label combos/metric

**GET /v1/query** - Query metrics
```bash
curl "http://localhost:8080/v1/query?metric=http_requests_total&start=2025-11-18T00:00:00Z"
```

**GET /v1/stats** - Storage statistics

**GET /v1/cardinality** - Cardinality usage

**GET /metrics** - Prometheus export (for Grafana)

## WebSocket Real-Time

```javascript
const ws = new WebSocket('ws://localhost:8080/ws');
ws.onmessage = (e) => console.log(JSON.parse(e.data));
```

Features: broadcasts to all clients, 30s ping/pong keepalive, auto-reconnect

## Cardinality Protection

Prevents too many unique label combinations:
- Tracks series per metric
- Rejects when limit hit (default: 1000)
- Returns HTTP 429

```
✅ Good: {service="api", endpoint="/users", status="200"}
❌ Bad:  {service="api", user_id="12345"}  // Creates infinite series!
```

## Performance

- Ingest: ~50k metrics/sec
- Query: ~10ms for 24h range
- WebSocket: <1ms broadcast latency

## Test Coverage: 13.5%

Needs WebSocket and dashboard endpoint tests.
