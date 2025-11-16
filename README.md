# TinyObs

A simple observability platform for learning how monitoring systems work.

[![Go 1.21+](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

TinyObs is a metrics collection system built in Go. It's designed to be small enough to understand completely, but useful enough for real local development. Think of it as a teaching-focused alternative to Prometheus—you can actually read all the code and understand how it works.

**What it does:**
- Collects metrics (counters, gauges, histograms) from your apps
- Stores them in memory (persistent storage coming soon)
- Shows real-time data on a web dashboard
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

# Browser: Open http://localhost:8080
```

You should see metrics appearing in real-time.

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
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Your App      │    │   TinyObs SDK   │    │  Ingest Server  │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │  Metrics    │─┼────┼─│   Batcher   │─┼────┼─│  In-Memory  │ │
│ │  Counter    │ │    │ │ (5s flush)  │ │    │ │   Storage   │ │
│ │  Gauge      │ │    │ └─────────────┘ │    │ └─────────────┘ │
│ │  Histogram  │ │    │                 │    │                 │
│ └─────────────┘ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│                 │    │ │HTTP Transport│─┼────┼─│   REST API  │ │
│ ┌─────────────┐ │    │ └─────────────┘ │    │ └─────────────┘ │
│ │ Middleware  │ │    │                 │    │                 │
│ │Auto-metrics │ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ └─────────────┘ │    │ │Runtime Stats│ │    │ │  Dashboard  │ │
│                 │    │ └─────────────┘ │    │ │  (Web UI)   │ │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

The SDK batches metrics and sends them every 5 seconds. The server stores everything in memory and serves a web dashboard that polls for updates.

## API

### POST /v1/ingest

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

### GET /v1/metrics

Returns all stored metrics as JSON.

## Project Structure

```
tinyobs/
├── cmd/
│   ├── server/          # Ingest server
│   └── example/         # Example app
├── pkg/
│   ├── sdk/
│   │   ├── client.go    # Main SDK client
│   │   ├── batch/       # Batching logic
│   │   ├── metrics/     # Counter, Gauge, Histogram
│   │   ├── httpx/       # HTTP middleware
│   │   ├── runtime/     # Runtime metrics collector
│   │   └── transport/   # HTTP transport
│   └── ingest/
│       └── handler.go   # Server handlers
└── web/
    └── index.html       # Dashboard
```

## The 53-Day Bug

During development, I accidentally left the server running for 53 days straight. When I finally noticed, it had collected 2.9 million metrics and was using 4.5 GB of RAM.

Turns out, closing your laptop doesn't kill background processes on macOS. The example app kept running through sleep/wake cycles, sending metrics every 2 seconds for almost two months.

**What I learned:**
- In-memory storage without retention policies = unbounded growth
- Memory leaks are silent—systems slow down gradually but don't crash
- Even simple projects need production patterns
- macOS is really good at keeping processes alive

This bug is now driving the roadmap. The next version will have data retention policies, persistent storage, and cardinality limits to prevent this kind of thing.

## Known Issues

**Memory grows forever:** The server keeps all metrics in memory with no cleanup. You need to restart it periodically.

**No persistence:** Restarting loses all data.

**Basic dashboard:** Just shows counts, no time-series graphs yet.

**Must run from project directory:** The server looks for `./web/` relative to where you run it.

## Roadmap

### Phase 1: Production Foundations (next 2-3 weeks)
- [ ] Data retention policies (fix the memory leak)
- [ ] Time-series charts with Chart.js
- [ ] Prometheus `/metrics` endpoint (Grafana compatibility)
- [ ] Better example app with multiple endpoints

### Phase 2: Persistent Storage (3-5 weeks)
- [ ] BadgerDB integration (LSM-based storage)
- [ ] Cardinality protection (prevent label explosion)
- [ ] Query API with time-range filtering
- [ ] Downsampling (store raw data → 5m aggregates → 1h aggregates)

### Phase 3: Advanced Features (2-3 months)
- [ ] Alerting system with webhooks
- [ ] PromQL query language (subset)
- [ ] Performance benchmarks
- [ ] Distributed tracing support

### Phase 4: Production-Ready (4-6 months)
- [ ] Multi-tenancy
- [ ] High availability and clustering
- [ ] Cloud storage backends (S3, GCS, MinIO)
- [ ] Service topology visualization

See [ROADMAP.md](ROADMAP.md) for detailed plans.

## Why TinyObs?

Most observability systems are impossible to learn from:
- **Prometheus** has 300k+ lines of code with complex TSDB internals
- **Datadog** is closed source
- **VictoriaMetrics** is production-focused, not teaching-focused

TinyObs is different. It's about 1,500 lines of readable Go code. Every design decision is documented. The code comments explain *why*, not just *what*.

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
