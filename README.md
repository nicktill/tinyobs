# TinyObs 🔍

A lightweight observability SDK and platform for Go applications. Get started with metrics in under 5 lines of code!

## Features

- **Lightweight SDK** with Counter, Gauge, and Histogram metrics
- **HTTP middleware** for automatic request latency and error tracking
- **Runtime metrics** collection (heap, goroutines, GC stats)
- **Batching and flushing** for efficient metric transmission
- **Ingest server** with REST API
- **Minimal dashboard** for real-time metric visualization

## Quick Start

### 1. Start the Ingest Server

```bash
go run cmd/server/main.go
```

The server will start on `http://localhost:8080` with the dashboard available at the root.

### 2. Add TinyObs to Your App

```go
package main

import (
    "context"
    "net/http"
    "tinyobs/pkg/sdk"
    "tinyobs/pkg/sdk/httpx"
)

func main() {
    // Initialize TinyObs (1 line)
    client, _ := sdk.New(sdk.ClientConfig{
        Service: "my-app",
        Endpoint: "http://localhost:8080/v1/ingest",
    })
    
    // Start client (1 line)
    client.Start(context.Background())
    defer client.Stop()
    
    // Add HTTP middleware (1 line)
    http.Handle("/", httpx.Middleware(client)(http.HandlerFunc(handler)))
    
    // Create custom metrics (2 lines)
    counter := client.Counter("my_requests_total")
    counter.Inc("endpoint", "/api")
    
    http.ListenAndServe(":3001", nil)
}
```

### 3. View Metrics

Open `http://localhost:8080` to see your metrics in real-time!

## Example Application

Run the included example to see TinyObs in action:

```bash
# Terminal 1: Start the ingest server
go run cmd/server/main.go

# Terminal 2: Start the example app
go run cmd/example/main.go
```

Then visit:
- Example app: `http://localhost:3001`
- Dashboard: `http://localhost:8080`

## API Reference

### Client Configuration

```go
type ClientConfig struct {
    Service   string        // Service name (required)
    APIKey    string        // API key for authentication
    Endpoint  string        // Ingest endpoint URL
    FlushEvery time.Duration // How often to flush metrics
}
```

### Metrics

#### Counter
```go
counter := client.Counter("requests_total")
counter.Inc()                    // Increment by 1
counter.Add(5)                   // Add 5
counter.Inc("method", "GET")     // With labels
```

#### Gauge
```go
gauge := client.Gauge("active_connections")
gauge.Set(42)                    // Set to 42
gauge.Inc()                      // Increment by 1
gauge.Dec()                      // Decrement by 1
gauge.Add(10)                    // Add 10
gauge.Sub(5)                     // Subtract 5
```

#### Histogram
```go
histogram := client.Histogram("request_duration_seconds")
histogram.Observe(0.1)           // Record 100ms
histogram.Observe(0.5, "method", "POST") // With labels
```

### HTTP Middleware

```go
// Automatically tracks request count, duration, and errors
handler := httpx.Middleware(client)(yourHandler)
```

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Your App      │    │   TinyObs SDK   │    │  Ingest Server  │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │   Metrics   │─┼────┼─│   Batcher   │─┼────┼─│   Storage   │ │
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │HTTP Handler │─┼────┼─│  Transport  │─┼────┼─│  Dashboard  │ │
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## Performance

- **Sub-100ms p99 SDK overhead** for metric collection
- **10k+ metrics/sec** ingest capacity on single instance
- **Efficient batching** reduces network overhead
- **Minimal memory footprint** with configurable batch sizes

## Quick Commands

Use the included Makefile for common tasks:

```bash
# Start the ingest server
make server

# Start the example application  
make example

# Build all binaries
make build

# Run tests
make test

# See all available commands
make help
```

## Development

### Project Structure

```
tinyobs/
├── cmd/
│   ├── server/          # Ingest server binary
│   └── example/         # Demo app using SDK
├── pkg/
│   ├── sdk/
│   │   ├── client.go    # Main SDK client
│   │   ├── metrics/     # Metric types (Counter, Gauge, Histogram)
│   │   ├── httpx/       # HTTP middleware
│   │   ├── runtime/     # Runtime metrics collector
│   │   ├── batch/       # Batching and flushing
│   │   └── transport/   # Network transport
│   └── ingest/
│       └── handler.go   # Ingest server handler
└── web/                 # Dashboard (HTML/JS)
```

### Building

```bash
# Build ingest server
go build -o tinyobs-server cmd/server/main.go

# Build example app
go build -o tinyobs-example cmd/example/main.go
```

## License

MIT License - see LICENSE file for details.
