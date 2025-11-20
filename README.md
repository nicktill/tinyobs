# TinyObs

**A metrics platform you can actually understand.**

[![Go 1.23+](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![TinyObs Dashboard](screenshots/dashboard-dark-theme-view.png)

I built TinyObs because I wanted to understand how observability systems work. Prometheus has 300k+ lines of code. I wanted something smaller that I could actually read and learn from.

TinyObs is a metrics platform in ~5,300 lines of Go (excluding tests and blank lines). It's small enough to read through in a weekend, and works well for local development. You get metrics collection, storage, downsampling, a dashboard, and an SDK.

**What you get:**
- Push-based metrics SDK (counters, gauges, histograms)
- Persistent storage with BadgerDB
- Automatic downsampling: raw → 5min → 1hr aggregates
- Dashboard with light/dark themes and keyboard shortcuts
- Query API with auto-downsampling based on time range
- `/metrics` endpoint for Prometheus/Grafana to scrape FROM TinyObs
- Readable code with comments

**Why you might want this:**
- You want to learn how metrics systems work
- You need local metrics during development
- You're learning Go and want to see a real-world codebase

## Quick Start

Get it running in 30 seconds:

```bash
# Clone the repo
git clone https://github.com/nicktill/tinyobs.git
cd tinyobs

# Terminal 1: Start TinyObs server
go run ./cmd/server

# Terminal 2: Run the example app (generates fake metrics)
go run ./cmd/example

# Terminal 3: Open the dashboard
open http://localhost:8080
```

You should see charts populating with fake API traffic. The example app simulates a web service with random latencies and occasional errors. Press `T` to toggle between light and dark themes, or `?` to see all keyboard shortcuts.

## Screenshots

### Dashboard View
The main dashboard automatically groups metrics by service and displays time-series charts with auto-downsampling.

<table>
  <tr>
    <td width="50%">
      <b>Dark Theme</b><br/>
      <img src="screenshots/dashboard-dark-theme-view.png" alt="Dashboard - Dark Theme"/>
    </td>
    <td width="50%">
      <b>Light Theme</b><br/>
      <img src="screenshots/dashboard-light-theme-view.png" alt="Dashboard - Light Theme"/>
    </td>
  </tr>
</table>

### Explore View
Select and overlay multiple metrics on a single chart for comparison. Search, filter, and compare any combination of time series.

<table>
  <tr>
    <td width="50%">
      <b>Metric Selection</b><br/>
      <img src="screenshots/explore-dark-view.png" alt="Explore View - Metric Selection"/>
    </td>
    <td width="50%">
      <b>Multi-Metric Overlay</b><br/>
      <img src="screenshots/explore-light-view-with-graph.png" alt="Explore View - Multi-Metric Chart"/>
    </td>
  </tr>
</table>

### Key Features Shown
- 🎨 **Light/Dark Theme Toggle** - Seamless theme switching with localStorage persistence
- 🔍 **Smart Filtering** - Filter by service, endpoint, or metric name
- ⌨️ **Keyboard Shortcuts** - Navigate fast with shortcuts (D, E, R, T, /, ESC, 1-4)
- 📈 **Multi-Metric Overlays** - Compare multiple time series on one chart
- 🎯 **Auto-Downsampling** - Intelligent resolution selection based on time range
- 📊 **Stable Colors** - Consistent color assignment across refreshes

## Dashboard Features

### Keyboard Shortcuts
The dashboard includes powerful keyboard shortcuts for fast navigation:

| Key | Action |
|-----|--------|
| `D` | Switch to Dashboard view |
| `E` | Switch to Explore view |
| `R` | Refresh current view |
| `T` | Toggle light/dark theme |
| `/` | Focus search (Explore view) |
| `ESC` | Clear selection or unfocus input |
| `1-4` | Quick time range selection (1h, 6h, 24h, 7d) |

### Visual Features
- **Theme Toggle** - Click ☀️/🌙 in the header or press `T` to switch themes. Preference saved automatically.
- **Auto-Downsampling** - Charts automatically select the best resolution (raw/5m/1h) based on time range for optimal performance.
- **Multi-Metric Overlays** - Compare multiple time series on a single chart in Explore view.
- **Smart Filtering** - Filter metrics by service, endpoint, or metric name with auto-discovery.

## Using the SDK

### Installation

```bash
go get github.com/nicktill/tinyobs
```

### Basic Usage

Here's how to add TinyObs to your own Go app:

```go
package main

import (
    "context"
    "net/http"

    "github.com/nicktill/tinyobs/pkg/sdk"
    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
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

### Push-Based Model (Like Datadog/New Relic)

**Important:** TinyObs uses a **push model**, not a pull model like Prometheus.

- **Your apps** use the SDK to **push** metrics → TinyObs `/v1/ingest` endpoint
- TinyObs stores metrics in BadgerDB
- TinyObs exposes `/metrics` endpoint for Prometheus/Grafana to **pull FROM TinyObs**

**This is NOT Prometheus:**
- ❌ TinyObs does NOT scrape `/metrics` endpoints from your services
- ✅ Your apps push metrics to TinyObs using the SDK
- ✅ Optionally, Prometheus can scrape `/metrics` FROM TinyObs

```
┌─────────────────┐    ┌─────────────────┐    ┌──────────────────────────┐
│   Your App      │    │   TinyObs SDK   │    │    TinyObs Server        │
│                 │    │                 │    │                          │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌──────────────────────┐ │
│ │  Metrics    │─┼────┼─│   Batcher   │─┼────┼─│     REST API         │ │
│ │  Counter    │ │PUSH│ │ (5s flush)  │ │PUSH│ │  /v1/ingest          │ │
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
                                               │                          │
                                               │ ┌──────────────────────┐ │
                                               │ │  /metrics Endpoint   │ │
                                               │ │  (Prometheus compat) │◄─┐
                                               │ └──────────────────────┘ │ PULL
                                               └──────────────────────────┘ │
                                                                            │
                                                                  ┌─────────────┐
                                                                  │ Prometheus/ │
                                                                  │  Grafana    │
                                                                  │ (Optional)  │
                                                                  └─────────────┘
```

**How it works:**
1. SDK batches metrics and pushes every 5s via HTTP to TinyObs
2. Server stores in BadgerDB (LSM tree with Snappy compression)
3. Compaction runs hourly: raw → 5m → 1h aggregates (240x compression)
4. Dashboard queries with auto-downsampling for performance
5. Time-series charts update every 30s
6. (Optional) External tools like Prometheus can scrape TinyObs's `/metrics` endpoint

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
│   ├── compaction/          # Multi-resolution downsampling
│   │   ├── compactor.go     # Compaction engine
│   │   └── types.go         # Aggregate types and metadata
│   └── query/               # Query parsing and execution
└── web/
    ├── index.html           # Simple dashboard (legacy)
    └── dashboard.html       # Chart.js time-series dashboard
```

**Code stats:** ~5,300 lines of production Go code (excluding tests, comments, and blank lines)

## The 53-Day Bug

Early in development, I forgot to kill the server. I left it running on my laptop for 53 days.

When I finally noticed, TinyObs had collected 2.9 million metrics and was eating 4.5 GB of RAM. The example app had been dutifully sending fake API metrics every 2 seconds, through sleep cycles, OS updates, and countless lid closes. macOS just... kept it alive.

**Lessons learned the hard way:**
- In-memory storage without retention = your RAM becomes a black hole
- Memory leaks are silent killers. Things just get slower until you notice
- Even side projects need production patterns (retention policies, cardinality limits)
- macOS is *really* good at keeping background processes alive

This bug shaped the entire V2.0 roadmap. I added BadgerDB for persistent storage, retention policies, cardinality protection, and downsampling. The 53-day bug forced me to build a real system instead of a toy.

## What's Missing (And Why)

TinyObs is opinionated. I left stuff out on purpose to keep the codebase learnable.

**No query language yet:** You query one metric at a time. No `rate()` or `sum()` functions. PromQL-like queries are planned for V4.0, but honestly, I'm still figuring out the best way to implement them without making the code explode.

**No alerting:** You can see your metrics, but it won't email you when things break. V3.0 will add basic threshold alerts, but for now you're on your own.

**No live updates:** Dashboard refreshes every 30 seconds via polling. WebSockets would be cooler, but polling is way simpler to implement and understand. (Also on the V3.0 list.)

**Runs locally only:** No authentication, no HTTPS, no clustering. This is a local dev tool, not a production SaaS. If you want to run this in production, you'll need to add auth yourself.

**Path assumptions:** The server expects `./web/` and `./data/` to exist relative to where you run it. This is lazy, I know. Just run it from the project directory.


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

### ✅ V2.2 - Smart Dashboard (Complete!)
- [x] Multi-metric overlay charts (compare metrics on same chart)
- [x] Dashboard templates (Go Runtime, HTTP API, Database presets)
- [x] Label-based filtering UI with auto-discovery
- [x] Modern gradient UI with improved UX
- [x] Light/dark theme toggle with localStorage persistence
- [x] Enhanced keyboard shortcuts (D, E, R, T, /, ESC, 1-4)
- [x] Stable color assignment (no more flickering charts!)
- [x] Auto-scroll to selected metrics in Explore view

### 🚧 V2.3 - Dashboard Polish (In Progress)
- [ ] Export/import dashboard configurations (JSON)
- [ ] Time comparison view (compare to 24h ago)
- [ ] Comprehensive test coverage (SDK batching: 95.7% complete)
- [ ] Enhanced documentation (Architecture guide, package docs)

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

## Why I Built This

I've used Prometheus, Datadog, and New Relic professionally. They're great tools, but they're also black boxes. When something breaks or behaves weirdly, you're stuck Googling instead of actually understanding what's happening.

**The learning problem:**
- Prometheus: 300k+ lines of C++ and Go with gnarly TSDB internals
- Datadog/New Relic: Closed source, can't even look
- VictoriaMetrics: Production-focused, optimized to hell, hard to follow

I wanted something I could actually *understand*. So I built TinyObs with three rules:

1. **Small enough to read**: ~5,300 lines of Go (excluding tests). You can read it all in a weekend.
2. **Real enough to use**: Not a toy. Persistent storage, compression, downsampling, professional UI.
3. **Honest documentation**: Comments explain *why* decisions were made, not just what the code does.

This is what I wish existed when I was learning systems programming. If you're trying to understand how observability works, or you want a meaty portfolio project that shows real engineering thinking, this is for you.

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

I built this to learn, and I'd love for others to learn from it too. Contributions are welcome, especially from people who are new to Go or systems programming.

**If you're a beginner:**
- Look for issues labeled `good first issue` - these are specifically designed to be approachable
- Don't worry about making mistakes. I'll help you through the PR process
- Ask questions in issues. There are no dumb questions

**If you're experienced:**
- Look for `help wanted` issues where I could use design input
- Feel free to suggest architectural improvements, but keep in mind the goal is readability over optimization
- Documentation improvements are always appreciated

**General guidelines:**
- Write tests for new features
- Add comments explaining *why*, not just *what*
- Keep the codebase small - if a feature adds >500 lines, let's discuss first
- Run `go test ./...` before submitting

Fork it, break it, fix it, submit a PR. I'll do my best to review quickly and help you get it merged.

## Performance

TinyObs is designed for local development. Some rough numbers on a MacBook Pro:

- Writes: ~50k metrics/sec
- Queries: <500ms for typical dashboards
- Storage: Compression reduces disk usage significantly
- Memory: ~50 MB baseline + overhead per series
- Default limit: 10,000 series (configurable)

For production scale, use Prometheus or VictoriaMetrics.

## FAQ

**Q: Can I use this in production?**
A: TinyObs is built for local development and learning. For production, use Prometheus or similar tools.

**Q: How does it compare to Prometheus?**
A: Prometheus is production-grade with 300k+ lines. TinyObs is ~5,300 lines for learning. Use Prometheus for production.

**Q: Can I use it with Grafana?**
A: Yes! Point Prometheus at TinyObs's `/metrics` endpoint to scrape metrics that have been pushed to TinyObs. Then connect Grafana to Prometheus as usual.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - How TinyObs works
- [Package Docs](https://pkg.go.dev/github.com/nicktill/tinyobs) - Go package documentation

## Resources

- [Prometheus TSDB Design](https://github.com/prometheus/prometheus/blob/main/tsdb/docs/format/README.md) - Time-series internals
- [Gorilla Paper](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf) - Compression algorithm
- [Systems Performance](http://www.brendangregg.com/systems-performance-2nd-edition-book.html) - Observability fundamentals

## License

MIT - see [LICENSE](LICENSE)

---

Built by [@nicktill](https://github.com/nicktill)
