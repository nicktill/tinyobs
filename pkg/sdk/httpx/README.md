# httpx

HTTP middleware for automatic request instrumentation. Wraps your HTTP handlers to collect metrics without changing your code.

## Quick Start

```go
import (
    "net/http"

    "github.com/nicktill/tinyobs/pkg/sdk"
    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
)

func main() {
    // Create TinyObs client
    client, _ := sdk.New(sdk.ClientConfig{Service: "api"})
    client.Start(context.Background())
    defer client.Stop()

    // Create your handlers
    mux := http.NewServeMux()
    mux.HandleFunc("/", homeHandler)
    mux.HandleFunc("/users", usersHandler)
    mux.HandleFunc("/orders", ordersHandler)

    // Wrap with TinyObs middleware
    handler := httpx.Middleware(client)(mux)

    // Start server
    http.ListenAndServe(":8080", handler)
}
```

That's it! You now automatically track:
- Request counts by method, path, and status
- Request duration by method, path, and status

## What It Tracks

### 1. Request Count

**Metric:** `http_requests_total`
**Type:** Counter
**Labels:**
- `method` - HTTP method (GET, POST, PUT, DELETE, etc.)
- `path` - Request path (/users, /orders, etc.)
- `status` - Response status code (200, 404, 500, etc.)
- `service` - Your service name (from SDK config)

**Example:**
```
http_requests_total{method="GET", path="/users", status="200", service="api"} = 1547
http_requests_total{method="POST", path="/orders", status="201", service="api"} = 892
http_requests_total{method="GET", path="/users", status="404", service="api"} = 23
```

### 2. Request Duration

**Metric:** `http_request_duration_seconds`
**Type:** Histogram
**Labels:** Same as above (method, path, status, service)

**Example:**
```
http_request_duration_seconds{method="GET", path="/users", status="200"} = 0.034s
http_request_duration_seconds{method="POST", path="/orders", status="201"} = 0.142s
```

## How It Works

```
Request Flow:
─────────────

1. Request arrives
   │
   ├─▶ httpx.Middleware
   │   │
   │   ├─ Start timer
   │   │
   │   ├─ Wrap ResponseWriter (to capture status code)
   │   │
   │   ├─▶ Call next handler (your code)
   │   │   │
   │   │   └─▶ Your handler runs
   │   │
   │   ├─ Calculate duration
   │   │
   │   ├─ Record metrics:
   │   │  • counter.Inc("method", "GET", "path", "/users", "status", "200")
   │   │  • histogram.Observe(0.034, "method", "GET", "path", "/users", "status", "200")
   │   │
   │   └─▶ Response sent
```

## Response Writer Wrapping

The middleware wraps `http.ResponseWriter` to capture the status code:

```go
type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
    rw.statusCode = code  // Capture status
    rw.ResponseWriter.WriteHeader(code)
}
```

**Why?** The standard `http.ResponseWriter` doesn't expose the status code, so we need to wrap it.

## Usage Patterns

### Standard Library

```go
mux := http.NewServeMux()
mux.HandleFunc("/", handler)

wrapped := httpx.Middleware(client)(mux)
http.ListenAndServe(":8080", wrapped)
```

### With Gorilla Mux

```go
import "github.com/gorilla/mux"

router := mux.NewRouter()
router.HandleFunc("/users", usersHandler).Methods("GET")
router.HandleFunc("/orders", ordersHandler).Methods("POST")

wrapped := httpx.Middleware(client)(router)
http.ListenAndServe(":8080", wrapped)
```

### With Chi Router

```go
import "github.com/go-chi/chi/v5"

r := chi.NewRouter()
r.Get("/users", usersHandler)
r.Post("/orders", ordersHandler)

wrapped := httpx.Middleware(client)(r)
http.ListenAndServe(":8080", wrapped)
```

### Multiple Middleware

```go
// Chain multiple middleware
handler := loggingMiddleware(
    authMiddleware(
        httpx.Middleware(client)(mux),
    ),
)
```

**Best practice:** Put TinyObs middleware early in the chain to measure total request time including other middleware.

## What Gets Measured?

### Measured
✅ Total request time (including all middleware)
✅ Response status codes (including errors)
✅ All HTTP methods (GET, POST, PUT, DELETE, etc.)
✅ All paths (even 404s)

### Not Measured
❌ Request body size
❌ Response body size
❌ Headers
❌ Query parameters

**Why?** Keeping it simple. These are the most useful metrics for 90% of use cases.

## Performance

Overhead per request:
- **Timing**: ~100ns (time.Now() + subtraction)
- **Metric recording**: ~200ns (mutex + map operations)
- **Total**: ~300ns (~0.0003ms)

For a 10ms request, middleware adds **0.003% overhead**. Negligible.

## Cardinality Considerations

**Watch out for high-cardinality paths!**

```go
// ❌ BAD - creates infinite unique paths
router.HandleFunc("/users/{id}", handler)  // /users/1, /users/2, /users/3, ...

// ✅ GOOD - normalize paths first
router.HandleFunc("/users/:id", handler)  // /users/:id (single series)
```

Most routers (Gorilla, Chi) normalize path parameters automatically. If you're using `http.ServeMux`, you'll need to normalize manually or use cardinality limits.

## Filtering Paths

Sometimes you want to exclude certain paths from metrics:

```go
// Custom middleware to skip health checks
func SkipHealthChecks(client sdk.ClientInterface) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.URL.Path == "/health" || r.URL.Path == "/ping" {
                next.ServeHTTP(w, r)
                return
            }

            // Use TinyObs middleware for everything else
            httpx.Middleware(client)(next).ServeHTTP(w, r)
        })
    }
}
```

## Customizing Metrics

Want different metric names or labels? You can create your own middleware:

```go
func CustomMiddleware(client sdk.ClientInterface) func(http.Handler) http.Handler {
    counter := client.Counter("my_custom_requests")
    duration := client.Histogram("my_custom_duration")

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()

            wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}
            next.ServeHTTP(wrapped, r)

            // Custom labels
            counter.Inc(
                "endpoint", r.URL.Path,
                "user_agent", r.UserAgent(),  // Custom label!
            )

            duration.Observe(
                time.Since(start).Seconds(),
                "endpoint", r.URL.Path,
            )
        })
    }
}
```

## Common Questions

### Q: Does this work with all routers?

**A:** Yes! It's standard `http.Handler` middleware, so it works with:
- `http.ServeMux` (standard library)
- Gorilla Mux
- Chi
- Echo (with adapter)
- Gin (with adapter)
- Any router that accepts `http.Handler`

### Q: What if I don't call WriteHeader()?

**A:** The middleware defaults to `200 OK`:

```go
wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
```

This matches standard HTTP behavior (implicit 200 if not specified).

### Q: Can I track request/response body sizes?

**A:** Not built-in, but you can extend the middleware:

```go
// Wrap ResponseWriter to count bytes
type countingWriter struct {
    http.ResponseWriter
    statusCode int
    bytes      int
}

func (w *countingWriter) Write(b []byte) (int, error) {
    w.bytes += len(b)
    return w.ResponseWriter.Write(b)
}
```

### Q: How do I track only failed requests?

**A:** You can't exclude metrics at middleware level, but you can filter in queries:

```
http_requests_total{status=~"5.."} // Only 5xx errors
http_requests_total{status=~"[45].."} // 4xx + 5xx errors
```

## Debugging

### View Metrics in Dashboard

After running your app with middleware:
1. Open http://localhost:8080/dashboard.html
2. Look for `http_requests_total` and `http_request_duration_seconds`
3. Filter by service name

### Check Cardinality

```bash
curl http://localhost:8080/v1/cardinality
```

Look for `http_requests_total` and `http_request_duration_seconds`. If cardinality is >1000, you might have high-cardinality paths.

## Test Coverage

Coverage: **0.0%** (as of v2.2)

**This package needs tests!** Contributions welcome.

Suggested test cases:
- Status code capture
- Duration measurement
- Multiple requests
- Error handling

## Example: Full API Server

```go
package main

import (
    "context"
    "encoding/json"
    "net/http"

    "github.com/nicktill/tinyobs/pkg/sdk"
    "github.com/nicktill/tinyobs/pkg/sdk/httpx"
)

func main() {
    // Setup TinyObs
    client, _ := sdk.New(sdk.ClientConfig{Service: "users-api"})
    client.Start(context.Background())
    defer client.Stop()

    // Define handlers
    mux := http.NewServeMux()
    mux.HandleFunc("/users", usersHandler)
    mux.HandleFunc("/users/create", createUserHandler)
    mux.HandleFunc("/health", healthHandler)

    // Wrap with middleware
    handler := httpx.Middleware(client)(mux)

    // Start server
    http.ListenAndServe(":8080", handler)
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
    users := []string{"Alice", "Bob", "Charlie"}
    json.NewEncoder(w).Encode(users)
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(map[string]string{"status": "created"})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}
```

After running this, you'll see metrics like:
```
http_requests_total{method="GET", path="/users", status="200", service="users-api"}
http_requests_total{method="POST", path="/users/create", status="201", service="users-api"}
http_request_duration_seconds{method="GET", path="/users", status="200", service="users-api"} = 0.002s
```

## See Also

- `pkg/sdk/` - Main SDK client
- `pkg/sdk/metrics/` - Counter and Histogram implementations
- `web/dashboard.html` - Visualize your HTTP metrics
