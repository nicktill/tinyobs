# TinyObs Quick Start Guide

Get TinyObs running in minutes.

## Quick Start (3 Steps)

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
# List all metrics
curl http://localhost:8080/v1/metrics/list

# Query with time range
curl "http://localhost:8080/v1/query/range?metric=http_requests_total&start=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)&end=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# PromQL instant query
curl "http://localhost:8080/v1/query/instant?query=sum(http_requests_total)"

# Get storage stats
curl http://localhost:8080/v1/stats

# Health check
curl http://localhost:8080/v1/health
```

## Configuration

Set environment variables before starting the server:

```bash
# Port (default: 8080)
export PORT=3000

# Storage limit in GB (default: 1)
export TINYOBS_MAX_STORAGE_GB=5

# BadgerDB memory limit in MB (default: 48)
export TINYOBS_MAX_MEMORY_MB=128

# Then run
go run ./cmd/server
```

Or use inline:
```bash
PORT=3000 TINYOBS_MAX_STORAGE_GB=5 go run ./cmd/server
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

```bash
# Build binary
go build -o tinyobs ./cmd/server

# Run binary
./tinyobs

# Or with custom port
PORT=3000 ./tinyobs
```

## WebSocket Testing

The dashboard automatically connects to WebSocket at `/v1/ws` for real-time updates.

### Manual WebSocket Test
```bash
# Using wscat (install: npm install -g wscat)
wscat -c ws://localhost:8080/v1/ws
```

### Check Connection in Browser
1. Open `http://localhost:8080`
2. Open Developer Tools (F12) → Console
3. Look for: `WebSocket connected - real-time updates enabled`

## Troubleshooting

### Port Already in Use
```bash
PORT=3001 go run ./cmd/server
```

### No Metrics Showing
1. Verify example app is running: `curl http://localhost:3000/health`
2. Check server stats: `curl http://localhost:8080/v1/stats`
3. Check example app logs for errors
4. Wait a few seconds - metrics are sent every 5 seconds

### WebSocket Not Connecting
1. Check browser console for errors
2. Verify server health: `curl http://localhost:8080/v1/health`
3. Try `ws://127.0.0.1:8080/v1/ws` instead of `localhost`

### Storage Issues
```bash
# Check storage usage
curl http://localhost:8080/v1/stats

# Clean up data directory (WARNING: deletes all metrics)
rm -rf ./data/tinyobs/*
```

## Verification Checklist

- [ ] Server starts without errors
- [ ] Dashboard loads at `http://localhost:8080`
- [ ] Example app runs and sends metrics
- [ ] WebSocket connects (check browser console)
- [ ] Metrics appear in dashboard
- [ ] Real-time updates work (stats refresh every 5s)
- [ ] API endpoints respond correctly
- [ ] Tests pass: `go test ./...`

## Next Steps

1. Explore the dashboard features
2. Try different metric types (counters, gauges, histograms)
3. Test PromQL queries: `sum()`, `avg()`, `rate()`
4. Export metrics: `curl http://localhost:8080/v1/export?format=json`
5. Read the [README.md](README.md) for SDK usage
6. Check out the code to understand how it works!
