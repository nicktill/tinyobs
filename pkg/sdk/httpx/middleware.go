package httpx

import (
	"net/http"
	"strconv"
	"time"

	"tinyobs/pkg/sdk/metrics"
)

// Middleware creates HTTP middleware for request metrics
func Middleware(client metrics.ClientInterface) func(http.Handler) http.Handler {
	requestCounter := metrics.NewCounter("http_requests_total", client)
	requestDuration := metrics.NewHistogram("http_request_duration_seconds", client)
	
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			
			// Wrap the response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			
			// Call the next handler
			next.ServeHTTP(wrapped, r)
			
			// Record metrics
			duration := time.Since(start).Seconds()
			
			requestCounter.Inc(
				"method", r.Method,
				"path", r.URL.Path,
				"status", strconv.Itoa(wrapped.statusCode),
			)
			
			requestDuration.Observe(duration,
				"method", r.Method,
				"path", r.URL.Path,
				"status", strconv.Itoa(wrapped.statusCode),
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


