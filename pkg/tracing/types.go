package tracing

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// TraceID uniquely identifies a trace (request flow across services)
// 128-bit random ID (compatible with W3C Trace Context spec)
type TraceID string

// SpanID uniquely identifies a span within a trace
// 64-bit random ID
type SpanID string

// SpanKind indicates the role of a span in a trace
type SpanKind string

const (
	// SpanKindClient indicates a client-side span (outgoing request)
	SpanKindClient SpanKind = "client"
	// SpanKindServer indicates a server-side span (incoming request)
	SpanKindServer SpanKind = "server"
	// SpanKindInternal indicates an internal operation span
	SpanKindInternal SpanKind = "internal"
)

// SpanStatus indicates the status of a span
type SpanStatus string

const (
	// SpanStatusOK indicates successful completion
	SpanStatusOK SpanStatus = "ok"
	// SpanStatusError indicates an error occurred
	SpanStatusError SpanStatus = "error"
)

// Span represents a single operation within a distributed trace
type Span struct {
	// Unique identifiers
	TraceID  TraceID `json:"trace_id"`            // Identifies the entire trace
	SpanID   SpanID  `json:"span_id"`             // Identifies this span
	ParentID SpanID  `json:"parent_id,omitempty"` // Parent span ID (empty for root spans)

	// Timing
	StartTime time.Time     `json:"start_time"` // When the span started
	EndTime   time.Time     `json:"end_time"`   // When the span ended
	Duration  time.Duration `json:"duration"`   // Calculated duration (EndTime - StartTime)

	// Metadata
	Service   string     `json:"service"`   // Service that created this span
	Operation string     `json:"operation"` // Operation name (e.g., "GET /api/users")
	Kind      SpanKind   `json:"kind"`      // Span kind (client/server/internal)
	Status    SpanStatus `json:"status"`    // Completion status

	// Attributes (tags for filtering and analysis)
	Tags map[string]string `json:"tags,omitempty"`

	// Error information (if status is error)
	Error string `json:"error,omitempty"`
}

// Trace represents a complete distributed trace (collection of related spans)
type Trace struct {
	TraceID   TraceID   `json:"trace_id"`
	RootSpan  *Span     `json:"root_span"`
	Spans     []*Span   `json:"spans"`
	StartTime time.Time `json:"start_time"` // Earliest span start time
	EndTime   time.Time `json:"end_time"`   // Latest span end time
	Duration  time.Duration
	Services  []string `json:"services"` // List of services involved
}

// TraceContext carries trace information across process boundaries
// Compatible with W3C Trace Context (https://www.w3.org/TR/trace-context/)
type TraceContext struct {
	TraceID  TraceID `json:"trace_id"`
	SpanID   SpanID  `json:"span_id"`
	Sampled  bool    `json:"sampled"`   // Whether this trace should be sampled
	ParentID SpanID  `json:"parent_id"` // For propagating parent-child relationships
}

// NewTraceID generates a new random 128-bit trace ID
func NewTraceID() TraceID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		panic("failed to generate trace ID: " + err.Error())
	}
	return TraceID(hex.EncodeToString(b[:]))
}

// NewSpanID generates a new random 64-bit span ID
func NewSpanID() SpanID {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("failed to generate span ID: " + err.Error())
	}
	return SpanID(hex.EncodeToString(b[:]))
}

// NewTraceContext creates a new root trace context (starts a new trace)
func NewTraceContext() TraceContext {
	return TraceContext{
		TraceID: NewTraceID(),
		SpanID:  NewSpanID(),
		Sampled: true, // For now, sample everything
	}
}

// NewChildContext creates a child context from a parent
func (tc TraceContext) NewChildContext() TraceContext {
	return TraceContext{
		TraceID:  tc.TraceID,  // Same trace ID
		SpanID:   NewSpanID(), // New span ID
		ParentID: tc.SpanID,   // Parent is current span
		Sampled:  tc.Sampled,  // Inherit sampling decision
	}
}

// ToHTTPHeaders converts trace context to HTTP headers (W3C format)
// traceparent: 00-{trace-id}-{span-id}-{flags}
func (tc TraceContext) ToHTTPHeaders() map[string]string {
	flags := "00"
	if tc.Sampled {
		flags = "01"
	}

	return map[string]string{
		"traceparent": "00-" + string(tc.TraceID) + "-" + string(tc.SpanID) + "-" + flags,
	}
}

// ParseHTTPHeaders extracts trace context from HTTP headers
func ParseHTTPHeaders(headers map[string]string) (TraceContext, bool) {
	// Parse W3C traceparent header
	// Format: 00-{trace-id}-{span-id}-{flags}
	traceparent := headers["traceparent"]
	if traceparent == "" {
		traceparent = headers["Traceparent"] // Try capitalized
	}

	if traceparent == "" {
		return TraceContext{}, false
	}

	// Simple parsing (production would need more validation)
	var version, traceID, spanID, flags string
	if n, _ := fmt.Sscanf(traceparent, "%2s-%32s-%16s-%2s", &version, &traceID, &spanID, &flags); n == 4 {
		return TraceContext{
			TraceID:  TraceID(traceID),
			SpanID:   SpanID(spanID),
			ParentID: SpanID(spanID), // Parent is the incoming span
			Sampled:  flags == "01",
		}, true
	}

	return TraceContext{}, false
}
