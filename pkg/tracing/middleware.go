package tracing

import (
	"net/http"
	"strconv"
)

// HTTPMiddleware wraps an HTTP handler with distributed tracing
// It automatically:
// - Extracts trace context from incoming requests (W3C traceparent header)
// - Creates a server span for the request
// - Records request metadata (method, path, status code)
// - Injects trace context into the request context for downstream use
func HTTPMiddleware(tracer *Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from headers (if present)
			headers := make(map[string]string)
			for key := range r.Header {
				headers[key] = r.Header.Get(key)
			}

			var ctx = r.Context()
			var span *Span

			// Check for existing trace context in headers
			if traceCtx, ok := ParseHTTPHeaders(headers); ok {
				// Continue existing trace
				ctx = InjectTraceContext(ctx, traceCtx)
				ctx, span = tracer.StartSpan(ctx, r.Method+" "+r.URL.Path, SpanKindServer)
			} else {
				// Start new trace
				ctx, span = tracer.StartSpan(ctx, r.Method+" "+r.URL.Path, SpanKindServer)
			}

			// Add HTTP metadata to span
			span.Tags["http.method"] = r.Method
			span.Tags["http.url"] = r.URL.Path
			span.Tags["http.host"] = r.Host
			if r.RemoteAddr != "" {
				span.Tags["http.client_ip"] = r.RemoteAddr
			}

			// Wrap response writer to capture status code
			wrapper := &responseWriter{
				ResponseWriter: w,
				statusCode:     200, // Default status
			}

			// Execute handler with trace context
			next.ServeHTTP(wrapper, r.WithContext(ctx))

			// Record response metadata
			span.Tags["http.status_code"] = strconv.Itoa(wrapper.statusCode)
			span.Tags["http.response_size"] = strconv.FormatInt(wrapper.bytesWritten, 10)

			// Mark span as error if status >= 500
			if wrapper.statusCode >= 500 {
				span.Status = SpanStatusError
				span.Error = "HTTP " + strconv.Itoa(wrapper.statusCode)
			}

			// Finish span
			tracer.FinishSpan(ctx, span)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code and response size
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// InjectHeaders adds trace context to HTTP headers for outgoing requests
// Use this when making HTTP calls to other services to propagate the trace
func InjectHeaders(r *http.Request, tc TraceContext) {
	headers := tc.ToHTTPHeaders()
	for key, value := range headers {
		r.Header.Set(key, value)
	}
}

// HTTPClientMiddleware wraps an HTTP RoundTripper to inject trace context
// Usage:
//   client := &http.Client{
//       Transport: tracing.HTTPClientMiddleware(tracer, http.DefaultTransport),
//   }
func HTTPClientMiddleware(tracer *Tracer, next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}

	return &tracingRoundTripper{
		tracer: tracer,
		next:   next,
	}
}

type tracingRoundTripper struct {
	tracer *Tracer
	next   http.RoundTripper
}

func (t *tracingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create client span
	ctx, span := t.tracer.StartSpan(req.Context(), req.Method+" "+req.URL.Path, SpanKindClient)

	// Add HTTP metadata
	span.Tags["http.method"] = req.Method
	span.Tags["http.url"] = req.URL.String()
	span.Tags["http.host"] = req.Host

	// Inject trace context into headers
	if tc, ok := GetTraceContext(ctx); ok {
		InjectHeaders(req, tc)
	}

	// Execute request
	resp, err := t.next.RoundTrip(req.WithContext(ctx))

	// Record response
	if resp != nil {
		span.Tags["http.status_code"] = strconv.Itoa(resp.StatusCode)
		if resp.StatusCode >= 500 {
			span.Status = SpanStatusError
			span.Error = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
	}

	// Record error
	if err != nil {
		t.tracer.FinishSpanWithError(ctx, span, err)
	} else {
		t.tracer.FinishSpan(ctx, span)
	}

	return resp, err
}
