# TinyObs

**A lightweight metrics platform you can actually understand.** · [Website & docs](https://nicktill.github.io/tinyobs/)

[![Go 1.23+](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![TinyObs Dashboard](screenshots/dashboard-dark-theme-view.png)

TinyObs is a metrics platform in about 6,000 lines of Go (excluding tests and comments). Small enough to read in a weekend, useful enough for local development.

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
- **Persistent storage** on BadgerDB: ~14 bytes/sample, crash-safe, 72h retention by default
- **PromQL**, checked against Prometheus's own test suite ([what's supported](#promql-support))
- **Prometheus HTTP API**, so Grafana can use TinyObs as a Prometheus data source
- **Dashboard** for exploring your metrics

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

    // You can also create custom metrics for business logic:
    activeUsers := client.Gauge("active_users")
    activeUsers.Set(42.0) // Set current active users

    errors := client.Counter("errors_total")
    errors.Inc("type", "api_error", "endpoint", "/api/users")

    // Your app listens on its own port; TinyObs uses 8080.
    http.ListenAndServe(":3000", handler)
}
```

## API

TinyObs serves the [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/):
point Grafana's Prometheus data source at `http://localhost:8080`.

| Endpoint | Purpose |
|---|---|
| `GET/POST /api/v1/query`, `/api/v1/query_range` | PromQL queries |
| `GET/POST /api/v1/series`, `/api/v1/labels`, `GET /api/v1/label/<name>/values` | Series and label discovery |
| `GET /api/v1/metadata` | Metric type and help text |
| `GET /api/v1/status/tsdb`, `/api/v1/status/buildinfo` | Cardinality statistics, version |
| `POST /api/v1/write` | [Prometheus remote write](https://prometheus.io/docs/specs/prw/remote_write_spec/) 1.0 (Prometheus, Alloy, OTel Collector, vmagent) |
| `POST /v1/ingest` | Ingest from the TinyObs Go SDK |
| `GET /metrics` | TinyObs's own metrics: series vs. limits, rejected samples by reason, disk use |
| `GET /-/healthy`, `/-/ready` | Health checks |

Push from an existing Prometheus or Alloy agent:

```yaml
remote_write:
  - url: http://localhost:8080/api/v1/write
```

```bash
curl -s localhost:8080/api/v1/query --data-urlencode 'query=sum by (path) (rate(http_requests_total[5m]))'
```

### PromQL support

The engine is tested against [Prometheus's PromQL test suite](https://github.com/prometheus/prometheus/tree/v3.7.0/promql/promqltest/testdata)
(`make promql-compat`). Every supported feature returns Prometheus's result; everything else is an explicit
"not supported by TinyObs" error, never a guess.

- **Supported:** selectors with `=` `!=` `=~` `!~`, range vectors, `offset`, subqueries; arithmetic, comparison
  (with `bool`) and set operators with `on`/`ignoring`/`group_left`/`group_right`; `sum avg min max count group
  stddev stdvar topk bottomk quantile`; `rate irate increase delta idelta deriv predict_linear resets changes`,
  `*_over_time`, `histogram_quantile` (classic histograms), math, clamping, date and label functions.
- **Not supported:** native histograms, the `@` modifier, duration expressions, `count_values`, `limitk`,
  `holt_winters`, `sort_by_label`.

## Configuration

Environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `TINYOBS_LISTEN` | Listen address. Loopback by default; the Docker image uses `0.0.0.0:8080` | `127.0.0.1:$PORT` |
| `PORT` | Port, when `TINYOBS_LISTEN` is unset | `8080` |
| `TINYOBS_AUTH_TOKEN` | If set, required as `Authorization: Bearer <token>` on `/api/v1/write` and `/v1/ingest` | unset |
| `TINYOBS_DATA_DIR` | Data directory | `./data/tinyobs-v2` |
| `TINYOBS_RETENTION` | How long samples are kept | `72h` |
| `TINYOBS_MAX_SERIES` | Total series limit | `50000` |
| `TINYOBS_MAX_SERIES_PER_METRIC` | Series limit per metric name, so one label explosion can't starve the rest | `10000` |
| `TINYOBS_MAX_MEMORY_MB` | BadgerDB memory budget | `64` |

Disk use is bounded by `max series × samples per series in the retention window × ~14 bytes`.

Samples are never dropped silently: every rejection (out of order, series limit, per-metric limit,
invalid labels) is counted in `tinyobs_samples_rejected_total{reason}` on `/metrics`, and
`/api/v1/status/tsdb` lists the metrics and labels with the most series. The Go SDK's HTTP middleware
labels requests by the matched `ServeMux` route pattern and collapses unmatched (404) paths into one
value, so scanners can't mint series.

### Security

Reads are unauthenticated, so the dashboard and Grafana work without setup. TinyObs listens on
loopback by default and warns at startup if it is exposed without `TINYOBS_AUTH_TOKEN`. To accept
writes from other hosts, set a token (the Go SDK sends it via `ClientConfig.APIKey`, Prometheus via
`remote_write.authorization`) and keep the port on a private network.

### Staleness

Pushed series have no scrape to fail, so when a service stops, its series keep their last value
for the 5-minute lookback window and then disappear from instant queries.

## Project Structure

```
tinyobs/
├── cmd/
│   ├── server/     # Main server
│   └── example/    # Example app
├── pkg/
│   ├── api/        # Prometheus HTTP API
│   ├── labels/     # Series labels and matchers
│   ├── promql/     # Query engine
│   ├── sdk/        # Go client SDK
│   ├── server/     # HTTP server wiring
│   └── tsdb/       # Time series storage
└── web/            # Dashboard UI
```

## Why TinyObs?

I built this to understand how metrics systems work. Prometheus has 300k+ lines. TinyObs is about 6,000 lines you can actually read and learn from.

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
- [V2 design](docs/design/v2.md) - Architecture and the decisions behind it
- [ADRs](docs/adr/) - Storage layout, timestamp resolution

## Development

### Local Development

```bash
# Run tests (and the Prometheus conformance suite)
make test
make promql-compat

# Build
go build ./cmd/server

# Run with custom config
PORT=3000 TINYOBS_RETENTION=168h go run ./cmd/server
```

Docker: `make docker-up`, `make docker-demo`, `make docker-down`, `make docker-logs`.

See [QUICK_START.md](QUICK_START.md) for detailed instructions.

## License

MIT - see [LICENSE](LICENSE)

---

Built by [@nicktill](https://github.com/nicktill)
