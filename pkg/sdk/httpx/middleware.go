package httpx

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk"
)

// Middleware returns HTTP middleware that automatically tracks metrics.
// It tracks:
//   - http_requests_total (counter): by method, path, status
//   - http_request_duration_seconds (histogram): request latency
//
// Usage:
//
//	client, _ := sdk.New(sdk.ClientConfig{...})
//	client.Start(ctx)
//	defer client.Stop()
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("/", handler)
//	handler := httpx.Middleware(client)(mux)
//	http.ListenAndServe(":8080", handler)
func Middleware(client *sdk.Client) func(http.Handler) http.Handler {
	// Create metrics once (reused for all requests)
	requestCounter := client.Counter("http_requests_total")
	requestDuration := client.Histogram("http_request_duration_seconds")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap ResponseWriter to capture status code
			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Call the actual handler
			next.ServeHTTP(rw, r)

			// Calculate duration
			duration := time.Since(start).Seconds()

			normalizedPath := routeLabel(r, rw.statusCode)

			// Track metrics automatically
			statusStr := strconv.Itoa(rw.statusCode)
			requestCounter.Inc(
				"method", r.Method,
				"path", normalizedPath,
				"status", statusStr,
			)
			requestDuration.Observe(
				duration,
				"method", r.Method,
				"path", normalizedPath,
				"status", statusStr,
			)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// routeLabel returns a bounded-cardinality path label. It prefers the route
// pattern that net/http's ServeMux matched (Go 1.22+), e.g. "GET /users/{id}".
// Requests no route matched are collapsed into one value: otherwise every
// scanner probing /wp-login.php or /.env creates a new series.
func routeLabel(r *http.Request, status int) string {
	if r.Pattern != "" {
		if _, path, ok := strings.Cut(r.Pattern, " "); ok {
			return path
		}
		return r.Pattern
	}
	if status == http.StatusNotFound {
		return "unmatched"
	}
	return normalizePath(r.URL.Path)
}

var (
	uuidRe    = regexp.MustCompile(`(?i)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	numericRe = regexp.MustCompile(`/\d+`)
)

// normalizePath normalizes paths to avoid cardinality explosion.
// Examples:
//   - /api/users/123 → /api/users/{id}
//   - /posts/456/comments → /posts/{id}/comments
//   - /api/users/550e8400-e29b-41d4-a716-446655440000 → /api/users/{id}
func normalizePath(path string) string {
	// Replace UUIDs first (more specific pattern, case-insensitive)
	path = uuidRe.ReplaceAllString(path, "/{id}")

	// Replace numeric IDs with {id} (after UUIDs to avoid partial matches)
	path = numericRe.ReplaceAllString(path, "/{id}")

	return path
}
