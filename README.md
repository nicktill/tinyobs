# TinyObs

**A lightweight metrics platform you can actually understand.**

[![Go 1.23+](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![TinyObs Dashboard](screenshots/dashboard-dark-theme-view.png)

TinyObs is a metrics platform in ~5,000 lines of Go (excluding tests). Small enough to read in a weekend, useful enough for local development.

## Quick Start

### Option 1: Docker

```bash
# Start server only
make docker-up

# Or start server + example app (generates demo metrics)
make docker-demo

# View dashboard
open http://localhost:8080
```

**Alternative (without Make):**
```bash
docker-compose up -d                    # Server only
docker-compose --profile example up -d  # Server + example app
```

### Option 2: Local Development

```bash
# Terminal 1: Start server
go run ./cmd/server

# Terminal 2: Run example app (generates metrics)
go run ./cmd/example

# Terminal 3: Open dashboard
open http://localhost:8080
```

## What You Get

- **Push-based metrics SDK** (counters, gauges, histograms)
- **Persistent storage** with BadgerDB
- **Automatic downsampling**: raw → 5min → 1hr aggregates (240x compression)
- **Real-time dashboard** with WebSocket updates
- **Query API** with time-range filtering and aggregations
- **Export/Import** metrics (JSON, CSV)

## Using the SDK

```go
package main

import (
    "context"
    "net/http"
    "time"
    "github.com/nicktill/tinyobs/pkg/sdk"
    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
)

func main() {
    // Initialize TinyObs client
    client, _ := sdk.New(sdk.ClientConfig{
        Service:    "my-app",
        Endpoint:   "http://localhost:8080/v1/ingest",
        FlushEvery: 5 * time.Second,
    })
    
    ctx := context.Background()
    client.Start(ctx)
    defer client.Stop()
    
    // Create HTTP server
    mux := http.NewServeMux()
    mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("OK"))
    })
    
    // Add TinyObs middleware - automatically tracks:
    //   - http_requests_total (counter): by method, path, status
    //   - http_request_duration_seconds (histogram): request latency
    handler := httpx.Middleware(client)(mux)
    
    http.ListenAndServe(":8080", handler)
    
    // You can also create custom metrics for business logic:
    activeUsers := client.Gauge("active_users")
    activeUsers.Set(42.0) // Set current active users
    
    errors := client.Counter("errors_total")
    errors.Inc("type", "api_error", "endpoint", "/api/users")
}
```

## API Endpoints

- `POST /v1/ingest` - Ingest metrics
- `GET /v1/query/range` - Query metrics with time range
- `POST /v1/query/execute` - Execute query language queries
- `GET /v1/export` - Export metrics (JSON/CSV)
- `POST /v1/import` - Import metrics from backup
- `GET /v1/health` - Health check
- `GET /v1/ws` - WebSocket for real-time updates

**Prometheus-compatible endpoints (for Grafana):**
- `GET /api/v1/query` - Instant queries (Prometheus-compatible)
- `GET /api/v1/query_range` - Range queries (Prometheus-compatible)

See [QUICK_START.md](QUICK_START.md) for detailed API examples.

## Configuration

Environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `TINYOBS_MAX_STORAGE_GB` | Max storage in GB | `1` |
| `TINYOBS_MAX_MEMORY_MB` | BadgerDB memory limit | `48` |

## Project Structure

```
tinyobs/
├── cmd/
│   ├── server/     # Main server
│   └── example/    # Example app
├── pkg/
│   ├── sdk/        # Client SDK
│   ├── ingest/     # Metrics ingestion
│   ├── query/       # Query engine
│   ├── storage/     # BadgerDB storage
│   ├── compaction/ # Downsampling
│   └── export/     # Backup/restore
└── web/            # Dashboard UI
```

## Why TinyObs?

I built this to understand how metrics systems work. Prometheus has 300k+ lines. TinyObs is ~5,000 lines of core code you can actually read and learn from.

**Perfect for:**
- Learning how metrics systems work
- Local development metrics
- Understanding Go systems programming

**Not for:**
- Production deployments (use Prometheus)
- Distributed tracing (use Jaeger/Zipkin)
- Large-scale deployments

## Documentation

- [Quick Start Guide](QUICK_START.md) - Detailed setup and testing
- [Architecture](docs/ARCHITECTURE.md) - System design
- [Testing Guide](TESTING.md) - How to test

## Development

### Local Development

```bash
# Run tests
go test ./...

# Build
go build ./cmd/server

# Run with custom config
PORT=3000 TINYOBS_MAX_STORAGE_GB=5 go run ./cmd/server
```

### Docker

**Recommended (using Make):**
```bash
make docker-up      # Start server only
make docker-demo    # Start server + example app
make docker-down    # Stop all services
make docker-logs    # View logs
```

**Alternative (direct docker-compose):**
```bash
docker-compose up -d --build                    # Server only
docker-compose --profile example up -d --build  # Server + example app
docker-compose --profile example down           # Stop all services
docker-compose logs -f                          # View logs
```

See [QUICK_START.md](QUICK_START.md) for detailed instructions.

## License

MIT - see [LICENSE](LICENSE)

---

Built by [@nicktill](https://github.com/nicktill)
