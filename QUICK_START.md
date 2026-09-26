# TinyObs Quick Start Guide

Get TinyObs running in minutes.

## Quick Start Options

### Option 1: Docker

```bash
# Start server only
make docker-up

# Or start server + example app (generates demo metrics)
make docker-demo

# Wait a few seconds, then test
curl http://localhost:8080/-/healthy

# View dashboard
open http://localhost:8080
```

**Alternative (without Make):**
```bash
docker-compose up -d --build                    # Server only
docker-compose --profile example up -d --build  # Server + example app
```

Data persists in a Docker volume. The example app runs on port 3000 and sends metrics to the server.

### Option 2: Local Development (3 Steps)

### 1. Start the Server
```bash
# Terminal 1
go run ./cmd/server
```

Server starts on `http://localhost:8080`. Dashboard is available immediately.

### 2. Run the Example App
```bash
# Terminal 2
go run ./cmd/example
```

This starts a demo app on `:3000` that:
- Sends metrics to TinyObs every 5 seconds
- Generates simulated API traffic
- Demonstrates automatic HTTP metrics via `httpx.Middleware`

### 3. View the Dashboard
Open `http://localhost:8080` in your browser.

You should see:
- Real-time metrics visualization
- WebSocket connection status (green "Live" indicator)
- Stats updating every 5 seconds

## Testing the API

### Query Metrics
```bash
# List all metric names
curl http://localhost:8080/api/v1/label/__name__/values

# PromQL instant query
curl http://localhost:8080/api/v1/query --data-urlencode 'query=sum by (path) (rate(http_requests_total[5m]))'

# PromQL range query over the last hour (portable: Unix timestamps)
curl http://localhost:8080/api/v1/query_range --data-urlencode 'query=sum(rate(http_requests_total[1m]))' \
  -d start=$(($(date +%s) - 3600)) -d end=$(date +%s) -d step=60

# Cardinality: series per metric and label
curl http://localhost:8080/api/v1/status/tsdb
```

## Configuration

Set environment variables before starting the server:

```bash
# Port (default: 8080)
export PORT=3000

# Storage limit in GB (default: 1)
export TINYOBS_RETENTION=168h

# BadgerDB memory limit in MB (default: 48)
export TINYOBS_MAX_MEMORY_MB=128

# Then run
go run ./cmd/server
```

Or use inline:
```bash
PORT=3000 TINYOBS_RETENTION=168h go run ./cmd/server
```

## Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package
go test ./pkg/ingest/...
```

## Building

### Local Build

```bash
# Build binary
go build -o tinyobs ./cmd/server

# Run binary
./tinyobs

# Or with custom port
PORT=3000 ./tinyobs
```

### Docker Build

```bash
# Build and run with docker-compose
docker-compose up -d --build

# Or build manually
docker build -t tinyobs:latest .
docker run -d -p 8080:8080 -v tinyobs-data:/app/data tinyobs:latest
```

## Troubleshooting

### Port Already in Use
```bash
PORT=3001 go run ./cmd/server
```

### No Metrics Showing
1. Verify example app is running: `curl http://localhost:3000/health`
2. Check what TinyObs has stored: `curl http://localhost:8080/api/v1/status/tsdb`
3. Check example app logs for errors
4. Wait a few seconds - metrics are sent every 5 seconds

### Storage Issues
```bash
# Check series counts (disk use is bounded by series x retention)
curl http://localhost:8080/api/v1/status/tsdb

# Clean up data directory (WARNING: deletes all metrics)
rm -rf ./data/tinyobs-v2
```

## Verification Checklist

- [ ] Server starts without errors
- [ ] Dashboard loads at `http://localhost:8080`
- [ ] Example app runs and sends metrics
- [ ] Metrics appear in dashboard
- [ ] API endpoints respond correctly
- [ ] Tests pass: `go test ./...`

## Next Steps

1. Explore the dashboard features
2. Try different metric types (counters, gauges, histograms)
3. Test PromQL queries: `sum()`, `avg()`, `rate()`
4. Add TinyObs to Grafana as a Prometheus data source (`http://localhost:8080`)
5. Read the [README.md](README.md) for SDK usage
6. Check out the code to understand how it works!
