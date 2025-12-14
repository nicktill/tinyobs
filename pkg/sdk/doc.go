/*
Package sdk provides the TinyObs client library for instrumenting Go applications.

# Quick Start

Install TinyObs in your app:

	go get github.com/nicktill/tinyobs

Instrument your application:

	package main

	import (
	    "context"
	    "net/http"
	    "github.com/nicktill/tinyobs/pkg/sdk"
	    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
	)

	func main() {
	    // Create TinyObs client
	    client, err := sdk.New(sdk.ClientConfig{
	        Service:  "my-app",
	        Endpoint: "http://localhost:8080/v1/ingest",
	    })
	    if err != nil {
	        log.Fatal(err)
	    }

	    // Start client (begins batching and sending metrics)
	    client.Start(context.Background())
	    defer client.Stop()

	    // Wrap HTTP handlers for automatic metrics
	    mux := http.NewServeMux()
	    mux.HandleFunc("/", homeHandler)
	    handler := httpx.Middleware(client)(mux)

	    http.ListenAndServe(":8000", handler)
	}

This automatically tracks:
  - Request counts by endpoint, method, and status
  - Request duration histograms (p50, p95, p99)
  - Go runtime metrics (memory, goroutines, GC stats)

# Metric Types

TinyObs supports three metric types: Counter, Gauge, and Histogram.

Counter - Values that only increase:

	// Track total requests
	requests := client.Counter("http_requests_total")
	requests.Inc("endpoint", "/api/users", "method", "GET", "status", "200")

	// Increment by N
	requests.Add(5, "endpoint", "/api/posts")

Gauge - Values that go up and down:

	// Track active connections
	connections := client.Gauge("active_connections")
	connections.Inc()  // Connection opened
	connections.Dec()  // Connection closed
	connections.Set(42)  // Set to specific value

	// With labels
	queueSize := client.Gauge("queue_size")
	queueSize.Set(127, "queue", "emails", "priority", "high")

Histogram - Measure distributions:

	// Track request duration
	duration := client.Histogram("request_duration_seconds")
	duration.Observe(0.234, "endpoint", "/api/users")

	// Track response sizes
	responseSize := client.Histogram("response_bytes")
	responseSize.Observe(1024, "endpoint", "/api/posts")

Histograms automatically compute percentiles (p50, p95, p99, p999) on the server.

# Labels

Labels add dimensions to metrics for filtering and grouping:

	// Counter with labels
	requests := client.Counter("http_requests_total")
	requests.Inc("service", "api", "endpoint", "/users", "status", "200")

	// Gauge with labels
	temperature := client.Gauge("cpu_temperature_celsius")
	temperature.Set(67.5, "core", "0", "socket", "0")

	// Histogram with labels
	latency := client.Histogram("db_query_duration_seconds")
	latency.Observe(0.045, "database", "postgres", "table", "users", "operation", "select")

Labels must be provided as key-value pairs (even number of strings).

# Cardinality Warning

NEVER use high-cardinality values as labels:

	❌ user_id, request_id, email, UUID, timestamp
	✅ service, endpoint, method, status, env, region

Example of what NOT to do:

	// ❌ BAD: Creates a new time series for every user
	requests.Inc("user_id", "user_12345", "endpoint", "/api")
	// With 10,000 users → 10,000 time series → storage explosion

	// ✅ GOOD: Low cardinality
	requests.Inc("endpoint", "/api", "method", "GET", "status", "200")
	// Only creates one series per endpoint/method/status combo

High cardinality kills performance. Keep unique label combinations under 10,000.

# Batching & Flushing

The SDK batches metrics and sends them every 5 seconds (configurable):

	client, err := sdk.New(sdk.ClientConfig{
	    Service:    "my-app",
	    FlushEvery: 10 * time.Second,  // Custom flush interval
	})
	if err != nil {
	    log.Fatal(err)
	}

Metrics are buffered in memory until:
 1. FlushEvery duration elapses (default: 5 seconds), OR
 2. Batch reaches MaxBatchSize (default: 1000 metrics), OR
 3. You call client.Flush() manually

Manual flush:

	// Force immediate send (useful before shutdown)
	client.Flush()

	// Graceful shutdown (flushes pending metrics)
	client.Stop()

# Runtime Metrics

The SDK automatically collects Go runtime metrics every 15 seconds:

  - go_goroutines: Number of goroutines
  - go_cpu_count: Number of CPU cores
  - go_memory_heap_bytes: Heap memory usage
  - go_memory_stack_bytes: Stack memory usage
  - go_memory_sys_bytes: Total memory from OS
  - go_gc_count: Total GC runs
  - go_gc_pause_seconds: GC pause duration

No action needed - these are collected automatically when you call Start().

# HTTP Middleware

The httpx package provides automatic metrics for HTTP servers:

	import "github.com/nicktill/tinyobs/pkg/sdk/httpx"

	mux := http.NewServeMux()
	mux.HandleFunc("/", homeHandler)
	mux.HandleFunc("/api/users", usersHandler)

	// Wrap with TinyObs middleware
	handler := httpx.Middleware(client)(mux)
	http.ListenAndServe(":8000", handler)

This automatically tracks:
  - http_requests_total (counter): Requests by endpoint, method, status
  - http_request_duration_seconds (histogram): Request latency distribution

Endpoints are normalized to avoid cardinality explosion:
  - /api/users/123 → /api/users/{id}
  - /posts/456/comments → /posts/{id}/comments

# Client Configuration

ClientConfig supports several options:

	client, err := sdk.New(sdk.ClientConfig{
	    Service:    "my-app",              // Required: service name
	    Endpoint:   "http://localhost:8080/v1/ingest",  // TinyObs server URL
	    APIKey:     "secret-key",          // Optional: only set this if your TinyObs server requires authentication (typically in production)
	    FlushEvery: 5 * time.Second,       // How often to send batches
	})

	// Note: APIKey is only needed if your TinyObs server is configured to require authentication.
	// For local development or servers without authentication, you can omit this field.

Default values:
  - Endpoint: "http://localhost:8080/v1/ingest"
  - FlushEvery: 5 seconds
  - MaxBatchSize: 1000 metrics

# Error Handling

The SDK handles errors gracefully:

  - Network errors: Logged but don't crash your app
  - Server errors (5xx): Metrics are dropped (not retried)
  - Client errors (4xx): Metrics are dropped (invalid data)

Errors are logged to stderr. To capture errors:

	// Check client creation
	client, err := sdk.New(cfg)
	if err != nil {
	    log.Fatalf("Failed to create client: %v", err)
	}

	// Start doesn't return errors currently
	client.Start(ctx)

	// Stop flushes pending metrics
	client.Stop()

# Context & Cancellation

The SDK respects context cancellation:

	ctx, cancel := context.WithCancel(context.Background())
	client.Start(ctx)

	// Later: stop sending metrics
	cancel()
	client.Stop()  // Flushes remaining metrics

Use context.WithTimeout() for bounded shutdown:

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client.Start(ctx)
	defer client.Stop()  // Guaranteed to finish within 5 seconds

# Best Practices

1. Create one Client per application (singleton pattern)
2. Always call Start() before using metrics
3. Always call Stop() on shutdown (defer client.Stop())
4. Use low-cardinality labels only (service, endpoint, status)
5. Avoid creating metrics in hot loops (create once, reuse)
6. Call Flush() before exiting if you need guaranteed delivery

# Performance

The SDK is designed for low overhead:

  - Memory: ~5 KB per active metric
  - CPU: <1% overhead (M1 MacBook Pro, 10k metrics/sec)
  - Latency: <1µs per metric call (non-blocking)
  - Batching: Reduces network calls by 100x+

Metrics are added to an in-memory buffer instantly. Network sends happen asynchronously in the background.

# Example: Complete Application

	package main

	import (
	    "context"
	    "log"
	    "net/http"
	    "time"

	    "github.com/nicktill/tinyobs/pkg/sdk"
	    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
	)

	func main() {
	    // Create client
	    client, err := sdk.New(sdk.ClientConfig{
	        Service:  "my-api",
	        Endpoint: "http://localhost:8080/v1/ingest",
	    })
	    if err != nil {
	        log.Fatal(err)
	    }

	    // Start sending metrics
	    ctx := context.Background()
	    client.Start(ctx)
	    defer client.Stop()

	    // Create custom metrics
	    apiCalls := client.Counter("api_calls_total")
	    activeUsers := client.Gauge("active_users")
	    queryDuration := client.Histogram("db_query_duration_seconds")

	    // HTTP server with automatic metrics
	    mux := http.NewServeMux()
	    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
	        // Track custom business metrics
	        apiCalls.Inc("endpoint", "/", "version", "v1")
	        activeUsers.Inc()
	        defer activeUsers.Dec()

	        // Simulate DB query
	        start := time.Now()
	        // ... do work ...
	        queryDuration.Observe(time.Since(start).Seconds(), "query", "get_user")

	        w.Write([]byte("OK"))
	    })

	    // Wrap with TinyObs middleware for automatic HTTP metrics
	    handler := httpx.Middleware(client)(mux)

	    log.Println("Server starting on :8000")
	    http.ListenAndServe(":8000", handler)
	}

# See Also

  - pkg/sdk/httpx for HTTP middleware
  - pkg/sdk/metrics for metric type implementations
  - pkg/sdk/batch for batching logic
  - docs/ARCHITECTURE.md for how metrics flow through the system
*/
package sdk
