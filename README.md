# TinyObs 🔍

> A lightweight, production-minded observability platform for developers who want to understand how monitoring systems actually work.

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](#contributing)

**TinyObs** is a metrics collection and visualization platform built entirely in Go. It's small enough to understand completely, yet designed with production patterns from systems like Prometheus, VictoriaMetrics, and Datadog.

Perfect for:
- 🎓 **Learning** how time-series databases work under the hood
- 🔧 **Local development** monitoring without Docker or external services
- 📊 **Experimenting** with observability patterns before committing to vendor lock-in
- 💼 **Demonstrating** production-grade Go engineering skills

---

## ✨ Features

### Metrics SDK
- **Three metric types:** Counter, Gauge, Histogram
- **Automatic batching** for efficient network transmission
- **HTTP middleware** for zero-config request tracking
- **Runtime metrics** collection (heap, goroutines, GC stats)
- **Label support** for high-cardinality queries

### Ingest Server
- **REST API** for metric ingestion and querying
- **Real-time dashboard** with auto-refresh
- **In-memory storage** (persistent storage coming soon)
- **Service isolation** via automatic labeling
- **Zero dependencies** - single binary deployment

### Developer Experience
- **5 lines of code** to get started
- **No configuration files** required
- **Works offline** - no external services
- **Clean, readable code** designed for learning

---

## 🚀 Quick Start

### 1. Install and Run

```bash
# Clone the repository
git clone https://github.com/nicktill/tinyobs.git
cd tinyobs

# Start the server (Terminal 1)
go run cmd/server/main.go
# Server running on http://localhost:8080

# Start the example app (Terminal 2)
go run cmd/example/main.go
# Generating metrics on http://localhost:3001
```

### 2. View the Dashboard

Open your browser to **http://localhost:8080**

You should see metrics appearing in real-time! 🎉

### 3. Instrument Your Own App

```go
package main

import (
    "context"
    "net/http"
    "tinyobs/pkg/sdk"
    "tinyobs/pkg/sdk/httpx"
)

func main() {
    // Initialize TinyObs client
    client, _ := sdk.New(sdk.ClientConfig{
        Service:  "my-service",
        Endpoint: "http://localhost:8080/v1/ingest",
    })

    // Start collecting metrics
    client.Start(context.Background())
    defer client.Stop()

    // Add HTTP middleware for automatic instrumentation
    mux := http.NewServeMux()
    mux.HandleFunc("/", homeHandler)

    // Wrap with TinyObs middleware
    handler := httpx.Middleware(client)(mux)

    // Your app now tracks requests, latency, and errors automatically
    http.ListenAndServe(":8080", handler)
}
```

That's it! Your app now tracks:
- Request count by endpoint, method, and status code
- Request duration histograms
- Go runtime metrics (memory, goroutines, GC)

---

## 📊 Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Your App      │    │   TinyObs SDK   │    │  Ingest Server  │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │   Metrics   │─┼────┼─│   Batcher   │─┼────┼─│  In-Memory  │ │
│ │  Counter    │ │    │ │ (5s flush)  │ │    │ │   Storage   │ │
│ │  Gauge      │ │    │ └─────────────┘ │    │ └─────────────┘ │
│ │  Histogram  │ │    │                 │    │                 │
│ └─────────────┘ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│                 │    │ │HTTP Transport│─┼────┼─│   REST API  │ │
│ ┌─────────────┐ │    │ └─────────────┘ │    │ └─────────────┘ │
│ │  Middleware │ │    │                 │    │                 │
│ │Auto-tracking│ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ └─────────────┘ │    │ │Runtime Stats│ │    │ │  Dashboard  │ │
│                 │    │ └─────────────┘ │    │ │  (Web UI)   │ │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

**How it works:**

1. **Your app** creates metrics using the SDK (Counter, Gauge, Histogram)
2. **SDK batches** metric samples every 5 seconds
3. **HTTP transport** sends batches to the ingest server
4. **Server stores** metrics in memory and serves them via REST API
5. **Dashboard** polls the API and displays real-time updates

---

## 📖 Usage Examples

### Counter - Track Events

```go
// Track HTTP requests
requestCounter := client.Counter("http_requests_total")
requestCounter.Inc("endpoint", "/api/users", "method", "GET")

// Track errors
errorCounter := client.Counter("errors_total")
errorCounter.Inc("type", "database_timeout")
```

### Gauge - Measure Current State

```go
// Track active connections
activeConnections := client.Gauge("active_connections")
activeConnections.Inc()  // Connection opened
activeConnections.Dec()  // Connection closed

// Track queue depth
queueDepth := client.Gauge("queue_depth")
queueDepth.Set(42)
```

### Histogram - Measure Distributions

```go
// Track request duration
duration := client.Histogram("request_duration_seconds")
duration.Observe(0.234, "endpoint", "/api/users")

// Track response sizes
responseSize := client.Histogram("response_size_bytes")
responseSize.Observe(1024)
```

---

## 🔌 API Reference

### POST /v1/ingest

Send metrics to TinyObs.

**Request:**
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

**Response:**
```json
{
  "status": "success",
  "count": 1
}
```

### GET /v1/metrics

Query all stored metrics.

**Response:**
```json
{
  "metrics": [...],
  "count": 1234
}
```

---

## 🏗️ Project Structure

```
tinyobs/
├── cmd/
│   ├── server/              # Ingest server binary
│   │   └── main.go
│   └── example/             # Example app with metrics
│       └── main.go
├── pkg/
│   ├── sdk/
│   │   ├── client.go        # Main SDK client
│   │   ├── batch/           # Metric batching logic
│   │   ├── metrics/         # Counter, Gauge, Histogram
│   │   ├── httpx/           # HTTP middleware
│   │   ├── runtime/         # Go runtime metrics
│   │   └── transport/       # HTTP transport layer
│   └── ingest/
│       └── handler.go       # Ingest server HTTP handlers
└── web/
    └── index.html           # Real-time dashboard
```

---

## 🐛 The 53-Day Bug: A Production Learning Story

During initial development, the ingest server was accidentally left running for **53 days straight** (September 24 - November 16, 2025). When discovered, it had accumulated **2,905,632 metrics** and consumed **4.5 GB of RAM**.

### What Happened

The example app was started on September 24th and kept running continuously, sending metrics every 2 seconds. The process survived:
- Daily laptop sleep/wake cycles
- macOS system updates
- 53 days of continuous operation

### What We Learned

1. **macOS doesn't kill background processes** when you close your laptop
2. **In-memory storage without retention = unbounded growth**
3. **Memory leaks are silent killers** - the system gradually slowed but never crashed
4. **Even toy projects need production patterns** - retention policies aren't optional
5. **Monitoring the monitor matters** - observability systems need self-observation

### The Fix

This real-world discovery is driving the current roadmap:
- ✅ Add data retention policies (prevent unbounded growth)
- ✅ Implement persistent storage (survive restarts)
- ✅ Add cardinality limits (prevent label explosion)
- ✅ Build self-monitoring dashboard (track system health)

**This bug became the best teacher.** It revealed exactly what production observability systems must handle.

---

## 🗺️ Roadmap

TinyObs is evolving from a teaching tool into a production-capable platform. Here's the plan:

### ✅ Current (MVP - Shipped)
- [x] Metrics SDK (Counter, Gauge, Histogram)
- [x] HTTP middleware for auto-instrumentation
- [x] Batching and efficient transport
- [x] REST API for ingestion
- [x] Real-time web dashboard
- [x] Runtime metrics collection

### 🚧 Phase 1: Production Foundation (Next 2-3 weeks)
- [ ] **Data retention policies** - Fix the 53-day bug (in-memory cleanup)
- [ ] **Time-series charts** - Replace static counts with Chart.js line graphs
- [ ] **Prometheus `/metrics` endpoint** - Grafana/Prometheus compatibility
- [ ] **Multi-endpoint example app** - Realistic traffic simulation

**Goal:** Make it visually compelling and ecosystem-compatible

### 🔨 Phase 2: Persistent Storage (3-5 weeks)
- [ ] **BadgerDB integration** - LSM-based time-series storage
- [ ] **Cardinality protection** - Prevent label explosion attacks
- [ ] **Query API** - Time-range filtering and label-based queries
- [ ] **Downsampling** - Multi-resolution data (raw → 5m → 1h)

**Goal:** Production-grade data management

### 🎯 Phase 3: Advanced Features (2-3 months)
- [ ] **Alerting system** - Threshold-based alerts with webhooks
- [ ] **PromQL subset** - Industry-standard query language
- [ ] **Performance benchmarks** - Prove scalability claims
- [ ] **Distributed tracing** - OpenTelemetry integration

**Goal:** Feature parity with commercial systems

### 🚀 Phase 4: Enterprise-Ready (4-6 months)
- [ ] **Multi-tenancy** - Tenant isolation and quotas
- [ ] **High availability** - Replication and clustering
- [ ] **Cloud storage** - S3/GCS/MinIO backends for cold storage
- [ ] **Advanced visualization** - Service topology, heatmaps

**Goal:** Production deployments at scale

See detailed implementation plans in [ROADMAP.md](ROADMAP.md)

---

## 🎓 Why TinyObs?

### For Learners

Most observability systems are black boxes:
- **Prometheus** - 300k+ lines of code, complex TSDB internals
- **Datadog** - Closed source, impossible to learn from
- **VictoriaMetrics** - Production-focused, not teaching-focused

**TinyObs is different:**
- ~1,500 lines of readable Go code
- Every design decision is documented
- Code comments explain *why*, not just *what*
- Real production patterns, explained simply

### For Practitioners

TinyObs demonstrates production-grade Go engineering:
- **LSM-based storage** (like Prometheus, VictoriaMetrics)
- **Cardinality management** (prevent DoS via label explosion)
- **Efficient batching** (reduce network overhead)
- **Lock-free patterns** (high-throughput ingestion)
- **Worker pools** (parallel processing)

Perfect for interviews and portfolio projects.

### For Projects

TinyObs is genuinely useful:
- **Zero config** - works out of the box
- **No dependencies** - single binary deployment
- **Prometheus compatible** - easy migration path
- **Self-contained** - no external services required

Great for local development, testing, and small-scale production.

---

## 🔧 Development

### Running from Source

```bash
# Terminal 1: Run server
go run cmd/server/main.go

# Terminal 2: Run example app
go run cmd/example/main.go

# Terminal 3: Make a test request
curl http://localhost:3001/api/users
```

### Running Tests

```bash
make test

# With coverage
go test ./... -cover -v

# Specific package
go test ./pkg/sdk/... -v
```

### Building Binaries

```bash
# Build all binaries
make build

# Build just the server
go build -o bin/server cmd/server/main.go

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 go build -o bin/server-linux cmd/server/main.go
```

### Make Commands

```bash
make server    # Run ingest server
make example   # Run example app
make build     # Build all binaries
make test      # Run tests
make clean     # Remove build artifacts
make help      # Show all commands
```

---

## 🤝 Contributing

Contributions are welcome! TinyObs is a learning project, so beginner-friendly issues are encouraged.

### How to Contribute

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes with tests
4. Run `go fmt` and `make test`
5. Commit your changes (`git commit -m 'Add amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

### Good First Issues

New to the project? Look for:
- [`good first issue`](https://github.com/nicktill/tinyobs/labels/good%20first%20issue) - Beginner-friendly tasks
- [`help wanted`](https://github.com/nicktill/tinyobs/labels/help%20wanted) - Community input needed
- [`documentation`](https://github.com/nicktill/tinyobs/labels/documentation) - Improve docs

### Development Guidelines

- **Write tests** for new features
- **Keep PRs focused** - one feature per PR
- **Update documentation** when changing APIs
- **Follow Go conventions** - use `go fmt`, handle errors
- **Add comments** for complex logic

---

## 📚 Resources

### Learning Materials

- [Prometheus TSDB Design](https://github.com/prometheus/prometheus/blob/main/tsdb/docs/format/README.md) - Time-series database internals
- [Gorilla Paper](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf) - Time-series compression algorithm
- [Systems Performance](http://www.brendangregg.com/systems-performance-2nd-edition-book.html) - Observability best practices

### Related Projects

- [Prometheus](https://prometheus.io/) - Industry-standard monitoring
- [VictoriaMetrics](https://victoriametrics.com/) - High-performance TSDB
- [Grafana](https://grafana.com/) - Visualization platform
- [OpenTelemetry](https://opentelemetry.io/) - Observability framework

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## 🙏 Acknowledgments

TinyObs is inspired by and learns from:
- **Prometheus** - for pioneering modern metrics collection
- **VictoriaMetrics** - for showing how to optimize time-series storage
- **Brendan Gregg** - for teaching observability fundamentals
- **The Go community** - for building amazing libraries

Special thanks to everyone who contributes, opens issues, or simply stars the project. Your support makes this better.

---

## 📧 Contact

**Nick Tillmann**
GitHub: [@nicktill](https://github.com/nicktill)
Project: [github.com/nicktill/tinyobs](https://github.com/nicktill/tinyobs)

---

## ⭐ Star History

If you find TinyObs useful for learning or building, please consider starring the project!

[![Star History Chart](https://api.star-history.com/svg?repos=nicktill/tinyobs&type=Date)](https://star-history.com/#nicktill/tinyobs&Date)

---

**Built with ❤️ by developers who believe observability should be understandable.**

*Last updated: November 2025*
