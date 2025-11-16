# TinyObs

A simple observability platform for learning how monitoring systems work.

[![Go 1.21+](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

TinyObs is a metrics collection system built in Go. It's designed to be small enough to understand completely, but useful enough for real local development. Think of it as a teaching-focused alternative to Prometheus—you can actually read all the code and understand how it works.

**What it does:**
- Collects metrics (counters, gauges, histograms) from your apps
- Stores them persistently with BadgerDB (LSM tree with compression)
- Multi-resolution storage: raw data → 5-minute → 1-hour aggregates (240x compression)
- Smart dashboard with multi-metric overlays, templates, and label filtering
- Query API with time-range filtering and auto-downsampling
- Prometheus-compatible `/metrics` endpoint for Grafana integration
- Provides a clean SDK for instrumenting Go applications

**What it's good for:**
- Learning how time-series databases work
- Local development monitoring without Docker
- Understanding observability patterns before committing to vendor lock-in
- Building a portfolio project that demonstrates real engineering skills

## Quick Start

```bash
# Clone and run
git clone https://github.com/nicktill/tinyobs.git
cd tinyobs

# Terminal 1: Start the server
go run cmd/server/main.go

# Terminal 2: Start the example app (generates metrics)
go run cmd/example/main.go

# Browser: Open the dashboard
open http://localhost:8080/dashboard.html
```

You should see time-series charts updating in real-time. The dashboard auto-refreshes every 30 seconds.

## Using the SDK

Here's how to add TinyObs to your own Go app:

```go
package main

import (
    "context"
    "net/http"
    "tinyobs/pkg/sdk"
    "tinyobs/pkg/sdk/httpx"
)

func main() {
    // Create a TinyObs client
    client, _ := sdk.New(sdk.ClientConfig{
        Service:  "my-app",
        Endpoint: "http://localhost:8080/v1/ingest",
    })

    client.Start(context.Background())
    defer client.Stop()

    // Wrap your HTTP handlers to get automatic metrics
    mux := http.NewServeMux()
    mux.HandleFunc("/", homeHandler)

    handler := httpx.Middleware(client)(mux)
    http.ListenAndServe(":8080", handler)
}
```

This automatically tracks:
- Request counts by endpoint, method, and status
- Request duration histograms
- Go runtime metrics (memory, goroutines, GC stats)

### Creating Custom Metrics

```go
// Counter - for things that only go up
requests := client.Counter("http_requests_total")
requests.Inc("endpoint", "/api/users", "method", "GET")

// Gauge - for values that go up and down
connections := client.Gauge("active_connections")
connections.Inc()  // connection opened
connections.Dec()  // connection closed

// Histogram - for measuring distributions
duration := client.Histogram("request_duration_seconds")
duration.Observe(0.234, "endpoint", "/api/users")
```

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌──────────────────────────┐
│   Your App      │    │   TinyObs SDK   │    │    Ingest Server         │
│                 │    │                 │    │                          │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌──────────────────────┐ │
│ │  Metrics    │─┼────┼─│   Batcher   │─┼────┼─│     REST API         │ │
│ │  Counter    │ │    │ │ (5s flush)  │ │    │ │  /v1/ingest          │ │
│ │  Gauge      │ │    │ └─────────────┘ │    │ │  /v1/query/range     │ │
│ │  Histogram  │ │    │                 │    │ └──────────────────────┘ │
│ └─────────────┘ │    │ ┌─────────────┐ │    │                          │
│                 │    │ │HTTP Transport│─┼────┼─│ ┌──────────────────┐ │
│ ┌─────────────┐ │    │ └─────────────┘ │    │ │  Storage Layer    │ │
│ │ Middleware  │ │    │                 │    │ │  ┌──────────────┐ │ │
│ │Auto-metrics │ │    │ ┌─────────────┐ │    │ │  │ Memory       │ │ │
│ └─────────────┘ │    │ │Runtime Stats│ │    │ │  │ BadgerDB LSM │ │ │
│                 │    │ └─────────────┘ │    │ │  └──────────────┘ │ │
└─────────────────┘    └─────────────────┘    │ └──────────────────┘ │
                                               │                          │
                                               │ ┌──────────────────────┐ │
                                               │ │  Compaction Engine   │ │
                                               │ │  Raw → 5m → 1h       │ │
                                               │ │  (240x reduction)    │ │
                                               │ └──────────────────────┘ │
                                               │                          │
                                               │ ┌──────────────────────┐ │
                                               │ │  Chart.js Dashboard  │ │
                                               │ │  /dashboard.html     │ │
                                               │ └──────────────────────┘ │
                                               └──────────────────────────┘
```

**How it works:**
1. SDK batches metrics and sends every 5s via HTTP
2. Server stores in BadgerDB (LSM tree with Snappy compression)
3. Compaction runs hourly: raw → 5m → 1h aggregates (240x compression)
4. Dashboard queries with auto-downsampling for performance
5. Time-series charts update every 30s

## API

TinyObs provides a REST API for ingesting and querying metrics:

### POST /v1/ingest
Ingest metrics from your application.

```json
{
  "metrics": [
    {
      "name": "http_requests_total",
      "type": "counter",
      "value": 42,
      "timestamp": "2025-11-16T01:30:00Z",
      "labels": {
        "service": "api-server",
        "endpoint": "/users",
        "status": "200"
      }
    }
  ]
}
```

### GET /v1/query
Query metrics with optional filtering.

```bash
curl "http://localhost:8080/v1/query?metric=cpu_usage&start=2025-11-16T00:00:00Z"
```

### GET /v1/query/range
Query metrics with time-range and auto-downsampling.

```bash
curl "http://localhost:8080/v1/query/range?metric=cpu_usage&start=2025-11-16T00:00:00Z&end=2025-11-16T23:59:59Z&maxPoints=1000"
```

**Parameters:**
- `metric` (required): Metric name to query
- `start` (optional): Start time (RFC3339 or 2006-01-02T15:04:05)
- `end` (optional): End time (RFC3339 or 2006-01-02T15:04:05)
- `maxPoints` (optional): Maximum data points to return (default: 1000, max: 5000)

Returns downsampled time-series data with automatic resolution selection (raw/5m/1h).

### GET /v1/metrics/list
List all available metric names from the last 24 hours.

```bash
curl "http://localhost:8080/v1/metrics/list"
```

### GET /v1/stats
Get storage statistics.

```bash
curl "http://localhost:8080/v1/stats"
```

Returns total metrics count, unique series count, storage size, and time range.

## Project Structure

```
tinyobs/
├── cmd/
│   ├── server/              # Ingest server (main.go + tests)
│   └── example/             # Example app that generates metrics
├── pkg/
│   ├── sdk/                 # Client SDK for instrumenting apps
│   │   ├── client.go        # Main SDK client
│   │   ├── batch/           # Batching logic (5s flush)
│   │   ├── metrics/         # Counter, Gauge, Histogram types
│   │   ├── httpx/           # HTTP middleware for auto-metrics
│   │   ├── runtime/         # Runtime metrics collector (Go stats)
│   │   └── transport/       # HTTP transport layer
│   ├── ingest/              # Server-side ingestion
│   │   ├── handler.go       # REST API handlers
│   │   └── dashboard.go     # Dashboard API endpoints
│   ├── storage/             # Pluggable storage layer
│   │   ├── interface.go     # Storage abstraction
│   │   ├── memory/          # In-memory storage (fast, ephemeral)
│   │   └── badger/          # BadgerDB storage (persistent, LSM)
│   └── compaction/          # Multi-resolution downsampling
│       ├── compactor.go     # Compaction engine
│       └── types.go         # Aggregate types and metadata
└── web/
    ├── index.html           # Simple dashboard (legacy)
    └── dashboard.html       # Chart.js time-series dashboard
```

**Code stats:** ~2,600 lines of production Go code (excluding tests)

## The 53-Day Bug

During development, I accidentally left the server running for 53 days straight. When I finally noticed, it had collected 2.9 million metrics and was using 4.5 GB of RAM.

Turns out, closing your laptop doesn't kill background processes on macOS. The example app kept running through sleep/wake cycles, sending metrics every 2 seconds for almost two months.

**What I learned:**
- In-memory storage without retention policies = unbounded growth
- Memory leaks are silent—systems slow down gradually but don't crash
- Even simple projects need production patterns
- macOS is really good at keeping processes alive

This bug is now driving the roadmap. The next version will have data retention policies, persistent storage, and cardinality limits to prevent this kind of thing.

## Known Issues & Limitations

**No query language:** You can only query one metric at a time. A PromQL-like query language is planned for V4.0.

**No alerting:** There's no way to get notified when metrics cross thresholds. Basic alerting is planned for V3.0.

**30-second refresh:** Dashboard polls every 30 seconds instead of live streaming. WebSocket support planned for V3.0.

**Must run from project directory:** The server looks for `./web/` and `./data/` relative to where you run it. Deploy with proper working directory.


## Roadmap

### ✅ V2.0 - Completed
- [x] Time-series charts with Chart.js
- [x] BadgerDB integration (LSM-based storage with Snappy compression)
- [x] Query API with time-range filtering (`/v1/query/range`)
- [x] Downsampling (raw → 5m → 1h aggregates, 240x compression)
- [x] Production-quality dashboard with auto-downsampling

### ✅ V2.1 - Polish (Completed)
- [x] Resolution-aware data retention (enable cleanup)
- [x] Cardinality protection (prevent label explosion)
- [x] Prometheus `/metrics` endpoint (Grafana compatibility)
- [x] Enhanced .gitignore for build artifacts

### 🚧 V2.2 - Smart Dashboard (In Progress)
- [x] Multi-metric overlay charts (compare metrics on same chart)
- [x] Dashboard templates (Go Runtime, HTTP API, Database presets)
- [x] Label-based filtering UI with auto-discovery
- [x] Modern gradient UI with improved UX
- [ ] Export/import dashboard configurations (JSON)
- [ ] Time comparison view (compare to 24h ago)

### 📅 V3.0 - Real-time & Anomaly Detection (Next 2-4 weeks)
- [ ] WebSocket live updates (replace 30s polling)
- [ ] Statistical anomaly detection (2σ from moving average)
- [ ] Visual anomaly indicators on charts (red zones)
- [ ] Simple threshold alerts (email/webhook)
- [ ] Alert history view

### 🎯 V4.0 - Query Language & Advanced Features (2-3 months)
- [ ] Simple PromQL-like query language
- [ ] Functions: `avg()`, `sum()`, `rate()`, `count()`
- [ ] Label matching: `{service="api"}`
- [ ] Time ranges: `[5m]`, `[1h]`
- [ ] Query builder UI in dashboard
- [ ] Performance benchmarks

### 🚀 V5.0+ - Production & Scale (4-6+ months)
- [ ] High availability and clustering
- [ ] Cloud storage backends (S3, GCS, MinIO)
- [ ] Authentication & API keys
- [ ] Multi-tenancy support
- [ ] Distributed tracing support (OpenTelemetry)
- [ ] Service topology visualization

## Why TinyObs?

Most observability systems are impossible to learn from:
- **Prometheus** has 300k+ lines of code with complex TSDB internals
- **Datadog** is closed source
- **VictoriaMetrics** is production-focused, not teaching-focused

TinyObs is different. It's ~2,600 lines of readable Go code (excluding tests). Every design decision is documented. The code comments explain *why*, not just *what*. You can read the entire codebase in an afternoon and understand how a real monitoring system works.

If you want to understand how monitoring systems work, read the source. If you want to build something for your portfolio that shows real engineering depth, fork it and add features.

## Development

```bash
# Run from source
go run cmd/server/main.go

# Run tests
make test
go test ./... -v

# Build binaries
make build
```

## Contributing

This is a learning project, so contributions are welcome—especially from beginners. If you're new to Go or observability, this is a good place to start.

Look for issues labeled:
- `good first issue` - Easy tasks for beginners
- `help wanted` - Community input needed
- `documentation` - Improve the docs

Fork, make changes, write tests, open a PR. I'm happy to review and help.

## Resources

- [Prometheus TSDB Design](https://github.com/prometheus/prometheus/blob/main/tsdb/docs/format/README.md) - Time-series internals
- [Gorilla Paper](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf) - Compression algorithm
- [Systems Performance](http://www.brendangregg.com/systems-performance-2nd-edition-book.html) - Observability fundamentals

## License

MIT - see [LICENSE](LICENSE)

---

Built by [@nicktill](https://github.com/nicktill)
