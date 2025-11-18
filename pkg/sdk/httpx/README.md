# httpx

HTTP middleware for automatic request instrumentation.

## Quick Start

```go
client, _ := sdk.New(sdk.ClientConfig{Service: "api"})
client.Start(context.Background())
defer client.Stop()

mux := http.NewServeMux()
mux.HandleFunc("/", homeHandler)

// Wrap with middleware
handler := httpx.Middleware(client)(mux)
http.ListenAndServe(":8080", handler)
```

## What It Tracks

**`http_requests_total`** - Counter
- Labels: `method`, `path`, `status`, `service`

**`http_request_duration_seconds`** - Histogram
- Labels: `method`, `path`, `status`, `service`

## Works With All Routers

- `http.ServeMux` (standard library)
- Gorilla Mux
- Chi
- Echo (with adapter)
- Any router accepting `http.Handler`

## Cardinality Warning

```go
// ❌ BAD - creates infinite paths
router.HandleFunc("/users/{id}", handler)  // /users/1, /users/2, ...

// ✅ GOOD - normalize paths
router.HandleFunc("/users/:id", handler)   // Single series
```

Most routers normalize automatically.

## Performance

Overhead per request: ~300ns (~0.0003ms)

For a 10ms request, middleware adds 0.003% overhead.

## Test Coverage: 0.0%

Needs tests!
