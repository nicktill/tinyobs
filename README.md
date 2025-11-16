# TinyObs

> A lightweight, developer-friendly observability platform built in Go

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

TinyObs is a simple, self-contained observability system designed for developers who want to understand how monitoring works under the hood. Built entirely in Go with zero external dependencies for the core system, it provides metric collection, storage, and visualization in a single binary.

## 🎯 What is TinyObs?

TinyObs is a **teaching-focused observability platform** that demonstrates the core concepts behind systems like Prometheus, Datadog, and New Relic. It's small enough to understand completely, but powerful enough to be genuinely useful for local development and testing.

### Key Features

- **📊 Metric Collection**: Ingest time-series metrics via HTTP API
- **💾 In-Memory Storage**: Fast, zero-config metric storage (persistence coming soon)
- **📈 Web Dashboard**: Real-time visualization of collected metrics
- **🔌 Simple API**: RESTful endpoints for sending and querying metrics
- **🚀 Single Binary**: No external dependencies, just compile and run
- **🎓 Educational**: Clean, readable code designed for learning

### What TinyObs is NOT (yet)

- ❌ Not production-ready for high-scale deployments
- ❌ No data persistence across restarts (SQLite support planned)
- ❌ Limited time-series visualization (Chart.js integration planned)
- ❌ No alerting or advanced features (see [Future Plans](#-future-plans))

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      TinyObs System                      │
├─────────────────────────────────────────────────────────┤
│                                                          │
│  ┌──────────────┐        ┌──────────────┐              │
│  │ Example App  │        │   Your App   │              │
│  │ (Port 3000)  │        │              │              │
│  └──────┬───────┘        └──────┬───────┘              │
│         │                       │                       │
│         │  POST /ingest         │                       │
│         └───────────┬───────────┘                       │
│                     ▼                                    │
│         ┌─────────────────────┐                         │
│         │   Ingest Server     │                         │
│         │   (Port 8080)       │                         │
│         │                     │                         │
│         │  ┌───────────────┐  │                         │
│         │  │  In-Memory    │  │                         │
│         │  │  Metrics      │  │                         │
│         │  │  Storage      │  │                         │
│         │  └───────────────┘  │                         │
│         │                     │                         │
│         │  ┌───────────────┐  │                         │
│         │  │  Web Server   │  │                         │
│         │  │  Dashboard    │  │                         │
│         │  └───────────────┘  │                         │
│         └─────────────────────┘                         │
│                     │                                    │
│                     │  HTTP                              │
│                     ▼                                    │
│         ┌─────────────────────┐                         │
│         │   Web Browser       │                         │
│         │   localhost:8080    │                         │
│         └─────────────────────┘                         │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

## 🚀 Quick Start

### Prerequisites

- Go 1.21 or higher
- A terminal
- A web browser

### Installation

```bash
# Clone the repository
git clone https://github.com/nicktill/tinyobs.git
cd tinyobs

# Build the project
make build

# Or build manually
go build -o server cmd/server/main.go
go build -o example cmd/example/main.go
```

### Running TinyObs

**Terminal 1 - Start the ingest server:**
```bash
cd ~/path/to/tinyobs
./server
```

You should see:
```
2025/11/16 01:34:26 Starting TinyObs server on :8080
```

**Terminal 2 - Start the example app (metric generator):**
```bash
cd ~/path/to/tinyobs
./example
```

You should see:
```
2025/11/16 01:30:49 Starting example app on :3000
2025/11/16 01:30:49 Visit http://localhost:3000 to see the app in action
2025/11/16 01:30:49 Visit http://localhost:8080 to see the TinyObs dashboard
```

**Browser - View the dashboard:**

Open your browser and navigate to:
```
http://localhost:8080
```

You should see metrics being collected in real-time! 🎉

### Using Make Commands

```bash
# Build all binaries
make build

# Run the server
make server

# Run the example app
make example

# Clean build artifacts
make clean

# Run tests
make test
```

## 📡 API Reference

### POST /ingest

Send metrics to TinyObs.

**Request:**
```json
POST http://localhost:8080/ingest
Content-Type: application/json

{
  "metrics": [
    {
      "name": "http_requests_total",
      "value": 42,
      "timestamp": "2025-11-16T01:30:00Z",
      "labels": {
        "endpoint": "/api/users",
        "method": "GET",
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

### GET /api/metrics

Query stored metrics.

**Request:**
```bash
curl http://localhost:8080/api/metrics
```

**Response:**
```json
{
  "total_metrics": 1234,
  "services": 1,
  "metrics": [
    {
      "name": "http_requests_total",
      "value": 42,
      "timestamp": "2025-11-16T01:30:00Z",
      "labels": {
        "endpoint": "/api/users",
        "method": "GET",
        "status": "200"
      }
    }
  ]
}
```

## 📂 Project Structure

```
tinyobs/
├── cmd/
│   ├── server/          # Main server entrypoint
│   │   └── main.go
│   └── example/         # Example metric generator
│       └── main.go
├── pkg/
│   ├── ingest/          # Metric ingestion handler
│   │   ├── handler.go
│   │   └── types.go
│   └── metrics/         # Metric generation utilities
│       └── collector.go
├── web/                 # Web dashboard
│   ├── index.html
│   └── styles.css
├── Makefile
├── go.mod
├── go.sum
└── README.md
```

## 🎓 How It Works

### 1. Metric Collection

The example app generates metrics every 2 seconds:
```go
// Collect runtime metrics
metrics := []Metric{
    {
        Name:  "go_memstats_heap_alloc_bytes",
        Value: float64(m.Alloc),
        Timestamp: time.Now(),
        Labels: map[string]string{"service": "example-app"},
    },
    // ... more metrics
}
```

### 2. Metric Ingestion

Metrics are sent to the server via HTTP POST:
```go
// Send batch of metrics to TinyObs
client.Post("http://localhost:8080/ingest", "application/json", metricsJSON)
```

### 3. In-Memory Storage

The server stores metrics in a simple Go slice:
```go
type Handler struct {
    metrics []Metric
    mu      sync.RWMutex
}

func (h *Handler) HandleIngest(w http.ResponseWriter, r *http.Request) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
    var req IngestRequest
    json.NewDecoder(r.Body).Decode(&req)
    
    // Append new metrics
    h.metrics = append(h.metrics, req.Metrics...)
}
```

### 4. Dashboard Visualization

The web dashboard polls the API every 5 seconds and updates the UI:
```javascript
async function fetchMetrics() {
    const response = await fetch('/api/metrics');
    const data = await response.json();
    updateDashboard(data);
}

// Auto-refresh every 5 seconds
setInterval(fetchMetrics, 5000);
```

## 🐛 Known Issues & Limitations

### Memory Leak (Critical)

**Issue:** The current implementation stores ALL metrics in memory forever, leading to unbounded growth.

**Impact:** 
- Server was left running for 53 days
- Accumulated 2.9 million metrics
- Consumed 4.5 GB of RAM
- Would eventually crash the system

**Temporary Workaround:** Restart the server periodically

**Permanent Fix:** See [Issue #1](https://github.com/nicktill/tinyobs/issues/1) - Data retention policy (in progress)

### No Persistence

**Issue:** All metrics are lost when the server restarts.

**Workaround:** Keep server running or use external monitoring.

**Fix:** SQLite storage backend (planned - see [Future Plans](#-future-plans))

### Limited Visualization

**Issue:** Dashboard only shows total counts, not time-series graphs.

**Workaround:** Query the API directly for detailed data.

**Fix:** Chart.js integration (planned)

### Working Directory Dependency

**Issue:** Server must be run from the `tinyobs/` directory or dashboard will 404.

**Workaround:** Always `cd` to the project directory before running `./server`

**Fix:** Embed web files or use absolute paths (planned)

## 📚 Production Learnings

This project was built as a learning exercise, and we discovered several real-world issues:

### The 53-Day Bug 🐞

During development, the ingest server was accidentally left running for **53 days** (Sept 24 - Nov 16, 2025), accumulating **2,905,632 metrics** and consuming **4.5 GB of RAM**. 

**What happened:**
- Example app started on September 24, 2025
- Process ran continuously (surviving sleep/wake cycles)
- Metrics accumulated every 2 seconds
- No data retention policy = unlimited growth
- Finally discovered on November 16, 2025

**What we learned:**
1. **macOS sleep mode preserves process state** - closing laptop doesn't kill processes
2. **Go processes can run for months** without issue if not explicitly killed
3. **Memory leaks are silent killers** - system gradually slows but doesn't crash immediately
4. **Data retention is critical** - even simple systems need cleanup policies
5. **Process monitoring matters** - `ps aux | grep /var/folders` reveals zombie processes

**Key Takeaways:**
- Always implement data retention policies
- Monitor your monitoring system
- Design for bounded resource usage
- Test long-running scenarios

This real-world discovery led to Issue #1 and is driving the SQLite persistence work.

## 🛠️ Development

### Running from Source

```bash
# Terminal 1: Run server with hot reload
go run cmd/server/main.go

# Terminal 2: Run example app
go run cmd/example/main.go
```

### Running Tests

```bash
# Run all tests
make test

# Run with coverage
go test ./... -cover

# Run specific package tests
go test ./pkg/ingest -v
```

### Building for Production

```bash
# Build optimized binaries
make build

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 go build -o server-linux cmd/server/main.go

# Cross-compile for macOS
GOOS=darwin GOARCH=arm64 go build -o server-mac cmd/server/main.go
```

## 🔮 Future Plans

TinyObs is under active development. Here's what's coming:

### Phase 1: Foundation (Current Focus)
- [x] Basic metric collection
- [x] Web dashboard
- [x] Example app
- [ ] **Data retention policy** (top priority - fixes memory leak)
- [ ] SQLite persistence
- [ ] Docker support

### Phase 2: Usability (Next 1-2 Months)
- [ ] **Multi-endpoint demo app** - Realistic traffic simulation with `/api/v1/users`, `/api/v1/products`, etc.
- [ ] **Time-series visualization** - Replace static counts with Chart.js line graphs
- [ ] **Data aggregation** - Downsample old data (1s → 1m → 1h resolution)
- [ ] Configurable retention policies (time-based and count-based)
- [ ] Health check endpoints
- [ ] Improved error handling

### Phase 3: Production-Ready (2-4 Months)
- [ ] **Prometheus export format** - `/metrics` endpoint compatible with Prometheus
- [ ] **Grafana integration** - Pre-built dashboard templates
- [ ] Redis storage backend option
- [ ] Query API with filtering and aggregation
- [ ] API authentication
- [ ] Rate limiting

### Phase 4: Advanced Features (4-6 Months)
- [ ] **Distributed tracing** - OpenTelemetry integration
- [ ] **Object storage support** - Cold storage for historical data (MinIO/S3)
- [ ] Alerting system (webhook notifications)
- [ ] Multi-tenancy support
- [ ] Cardinality control (prevent label explosion)
- [ ] PromQL query language support

### Phase 5: Enterprise (6+ Months)
- [ ] Machine learning anomaly detection
- [ ] Kubernetes operator
- [ ] High availability clustering
- [ ] Advanced visualization (heatmaps, flame graphs)
- [ ] Cost analytics and optimization

### Ideas & Research
- Embedded WASM-based query engine
- Plugin system for custom exporters
- Mobile app for dashboard
- AI-powered root cause analysis
- Natural language queries ("show me slow endpoints in the last hour")

See [ROADMAP.md](ROADMAP.md) for detailed implementation plans and timelines.

## 🤝 Contributing

Contributions are welcome! This is a learning project, so beginner-friendly issues are available.

### Getting Started

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Write tests
5. Commit your changes (`git commit -m 'Add amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

### Good First Issues

Looking for good first issues? Check out:
- [good first issue](https://github.com/nicktill/tinyobs/labels/good%20first%20issue) label
- [help wanted](https://github.com/nicktill/tinyobs/labels/help%20wanted) label
- [documentation](https://github.com/nicktill/tinyobs/labels/documentation) label

### Development Guidelines

- Write tests for new features
- Keep PRs focused and small
- Update documentation
- Follow Go best practices
- Add comments for complex logic
- Run `go fmt` before committing

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- Inspired by [Prometheus](https://prometheus.io/) - The industry-standard monitoring system
- Influenced by [VictoriaMetrics](https://victoriametrics.com/) - Fast, cost-effective monitoring
- Built with knowledge from [Brendan Gregg's Systems Performance](http://www.brendangregg.com/systems-performance-2nd-edition-book.html)

## 📧 Contact

Nick Tillmann - [@nicktill](https://github.com/nicktill)

Project Link: [https://github.com/nicktill/tinyobs](https://github.com/nicktill/tinyobs)

---

## 📊 Project Stats

- **Language:** Go 1.21+
- **Lines of Code:** ~1,500 (core system)
- **Dependencies:** Zero (for core - web UI uses vanilla JS)
- **Test Coverage:** TBD
- **Status:** Active Development (Alpha)

---

**⭐ If you find TinyObs useful, please consider giving it a star on GitHub!**

Built with ❤️ by [Nick Tillmann](https://github.com/nicktill)
