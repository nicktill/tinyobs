package tracing

import (
	"context"
	"sync"
	"time"
)

// contextKey is used for storing trace context in context.Context
type contextKey int

const (
	traceContextKey contextKey = iota
)

// Tracer manages distributed tracing
type Tracer struct {
	service string
	storage *Storage
	mu      sync.RWMutex
}

// NewTracer creates a new tracer for a service
func NewTracer(service string, storage *Storage) *Tracer {
	return &Tracer{
		service: service,
		storage: storage,
	}
}

// StartSpan starts a new span
// If ctx contains a trace context, this creates a child span
// Otherwise, this creates a root span (new trace)
func (t *Tracer) StartSpan(ctx context.Context, operation string, kind SpanKind) (context.Context, *Span) {
	var traceCtx TraceContext
	var parentID SpanID

	// Check if there's an existing trace context
	if existing, ok := ctx.Value(traceContextKey).(TraceContext); ok {
		// Create child span
		traceCtx = existing.NewChildContext()
		parentID = existing.SpanID
	} else {
		// Create new trace (root span)
		traceCtx = NewTraceContext()
		parentID = ""
	}

	// Create span
	span := &Span{
		TraceID:   traceCtx.TraceID,
		SpanID:    traceCtx.SpanID,
		ParentID:  parentID,
		StartTime: time.Now(),
		Service:   t.service,
		Operation: operation,
		Kind:      kind,
		Status:    SpanStatusOK,
		Tags:      make(map[string]string),
	}

	// Store trace context in context
	newCtx := context.WithValue(ctx, traceContextKey, traceCtx)

	return newCtx, span
}

// FinishSpan completes a span and stores it
func (t *Tracer) FinishSpan(ctx context.Context, span *Span) error {
	span.EndTime = time.Now()
	span.Duration = span.EndTime.Sub(span.StartTime)

	return t.storage.StoreSpan(ctx, span)
}

// FinishSpanWithError completes a span with an error
func (t *Tracer) FinishSpanWithError(ctx context.Context, span *Span, err error) error {
	span.Status = SpanStatusError
	span.Error = err.Error()
	return t.FinishSpan(ctx, span)
}

// SetTag adds a tag to a span
func (t *Tracer) SetTag(span *Span, key, value string) {
	if span.Tags == nil {
		span.Tags = make(map[string]string)
	}
	span.Tags[key] = value
}

// GetTraceContext extracts trace context from context.Context
func GetTraceContext(ctx context.Context) (TraceContext, bool) {
	tc, ok := ctx.Value(traceContextKey).(TraceContext)
	return tc, ok
}

// InjectTraceContext adds trace context to context.Context
func InjectTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey, tc)
}

// TraceFunc is a helper that automatically creates a span for a function
// Usage:
//   defer tracer.TraceFunc(ctx, "my_operation", SpanKindInternal)()
func (t *Tracer) TraceFunc(ctx context.Context, operation string, kind SpanKind) func() {
	_, span := t.StartSpan(ctx, operation, kind)

	return func() {
		// Use background context for storage since the original context might be cancelled
		t.FinishSpan(context.Background(), span)
	}
}

// TraceFuncWithError is like TraceFunc but records errors
// Usage:
//   var err error
//   defer tracer.TraceFuncWithError(ctx, "my_operation", SpanKindInternal, &err)()
func (t *Tracer) TraceFuncWithError(ctx context.Context, operation string, kind SpanKind, errPtr *error) func() {
	_, span := t.StartSpan(ctx, operation, kind)

	return func() {
		if errPtr != nil && *errPtr != nil {
			t.FinishSpanWithError(context.Background(), span, *errPtr)
		} else {
			t.FinishSpan(context.Background(), span)
		}
	}
}
