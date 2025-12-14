package httpx

import (
	"net/http"
	"regexp"
	"strconv"
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

			// Normalize path to avoid cardinality explosion
			normalizedPath := normalizePath(r.URL.Path)

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

// normalizePath normalizes paths to avoid cardinality explosion.
// Examples:
//   - /api/users/123 → /api/users/{id}
//   - /posts/456/comments → /posts/{id}/comments
//   - /api/users/abc-123-def → /api/users/{id}
func normalizePath(path string) string {
	// Replace numeric IDs with {id}
	re := regexp.MustCompile(`/\d+`)
	path = re.ReplaceAllString(path, "/{id}")

	// Replace UUIDs with {id}
	uuidRe := regexp.MustCompile(`/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	path = uuidRe.ReplaceAllString(path, "/{id}")

	return path
}
