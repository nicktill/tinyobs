package tracing

import (
	"context"
	"testing"
	"time"
)

func TestNewTracer(t *testing.T) {
	storage := NewStorage()
	tracer := NewTracer("test-service", storage)

	if tracer == nil {
		t.Fatal("Expected tracer to be created")
	}

	if tracer.service != "test-service" {
		t.Errorf("Expected service name 'test-service', got '%s'", tracer.service)
	}
}

func TestStartSpan(t *testing.T) {
	storage := NewStorage()
	tracer := NewTracer("test-service", storage)
	ctx := context.Background()

	// Start a root span
	newCtx, span := tracer.StartSpan(ctx, "test-operation", SpanKindServer)

	if span == nil {
		t.Fatal("Expected span to be created")
	}

	if span.Service != "test-service" {
		t.Errorf("Expected service 'test-service', got '%s'", span.Service)
	}

	if span.Operation != "test-operation" {
		t.Errorf("Expected operation 'test-operation', got '%s'", span.Operation)
	}

	if span.Kind != SpanKindServer {
		t.Errorf("Expected kind 'server', got '%s'", span.Kind)
	}

	if span.TraceID == "" {
		t.Error("Expected trace ID to be set")
	}

	if span.SpanID == "" {
		t.Error("Expected span ID to be set")
	}

	if span.ParentID != "" {
		t.Error("Expected parent ID to be empty for root span")
	}

	// Check that context has trace context
	if tc, ok := GetTraceContext(newCtx); !ok {
		t.Error("Expected trace context in returned context")
	} else {
		if tc.TraceID != span.TraceID {
			t.Error("Trace IDs don't match")
		}
		if tc.SpanID != span.SpanID {
			t.Error("Span IDs don't match")
		}
	}
}

func TestStartChildSpan(t *testing.T) {
	storage := NewStorage()
	tracer := NewTracer("test-service", storage)
	ctx := context.Background()

	// Start root span
	ctx, rootSpan := tracer.StartSpan(ctx, "root", SpanKindServer)

	// Start child span
	_, childSpan := tracer.StartSpan(ctx, "child", SpanKindInternal)

	if childSpan.TraceID != rootSpan.TraceID {
		t.Error("Child span should have same trace ID as root")
	}

	if childSpan.SpanID == rootSpan.SpanID {
		t.Error("Child span should have different span ID")
	}

	if childSpan.ParentID != rootSpan.SpanID {
		t.Errorf("Child span parent ID should be root span ID, got '%s' expected '%s'",
			childSpan.ParentID, rootSpan.SpanID)
	}
}

func TestFinishSpan(t *testing.T) {
	storage := NewStorage()
	tracer := NewTracer("test-service", storage)
	ctx := context.Background()

	// Start and finish span
	ctx, span := tracer.StartSpan(ctx, "test", SpanKindServer)
	time.Sleep(10 * time.Millisecond)
	err := tracer.FinishSpan(ctx, span)

	if err != nil {
		t.Fatalf("Failed to finish span: %v", err)
	}

	if span.EndTime.IsZero() {
		t.Error("Expected end time to be set")
	}

	if span.Duration == 0 {
		t.Error("Expected duration to be calculated")
	}

	if span.Duration < 10*time.Millisecond {
		t.Errorf("Expected duration >= 10ms, got %v", span.Duration)
	}

	// Verify span was stored
	trace, err := storage.GetTrace(ctx, span.TraceID)
	if err != nil {
		t.Fatalf("Failed to retrieve trace: %v", err)
	}

	if len(trace.Spans) != 1 {
		t.Errorf("Expected 1 span in storage, got %d", len(trace.Spans))
	}
}

func TestFinishSpanWithError(t *testing.T) {
	storage := NewStorage()
	tracer := NewTracer("test-service", storage)
	ctx := context.Background()

	ctx, span := tracer.StartSpan(ctx, "test", SpanKindServer)
	testErr := &testError{"test error"}
	err := tracer.FinishSpanWithError(ctx, span, testErr)

	if err != nil {
		t.Fatalf("Failed to finish span: %v", err)
	}

	if span.Status != SpanStatusError {
		t.Errorf("Expected status 'error', got '%s'", span.Status)
	}

	if span.Error != "test error" {
		t.Errorf("Expected error 'test error', got '%s'", span.Error)
	}
}

func TestSetTag(t *testing.T) {
	storage := NewStorage()
	tracer := NewTracer("test-service", storage)
	ctx := context.Background()

	_, span := tracer.StartSpan(ctx, "test", SpanKindServer)
	tracer.SetTag(span, "key1", "value1")
	tracer.SetTag(span, "key2", "value2")

	if span.Tags["key1"] != "value1" {
		t.Errorf("Expected tag 'key1' = 'value1', got '%s'", span.Tags["key1"])
	}

	if span.Tags["key2"] != "value2" {
		t.Errorf("Expected tag 'key2' = 'value2', got '%s'", span.Tags["key2"])
	}
}

func TestTraceContext(t *testing.T) {
	tc := NewTraceContext()

	if tc.TraceID == "" {
		t.Error("Expected trace ID to be set")
	}

	if tc.SpanID == "" {
		t.Error("Expected span ID to be set")
	}

	if !tc.Sampled {
		t.Error("Expected sampled to be true")
	}
}

func TestChildTraceContext(t *testing.T) {
	parent := NewTraceContext()
	child := parent.NewChildContext()

	if child.TraceID != parent.TraceID {
		t.Error("Child should have same trace ID as parent")
	}

	if child.SpanID == parent.SpanID {
		t.Error("Child should have different span ID")
	}

	if child.ParentID != parent.SpanID {
		t.Error("Child parent ID should be parent span ID")
	}

	if child.Sampled != parent.Sampled {
		t.Error("Child should inherit sampling decision")
	}
}

func TestHTTPHeaders(t *testing.T) {
	tc := NewTraceContext()
	headers := tc.ToHTTPHeaders()

	if headers["traceparent"] == "" {
		t.Error("Expected traceparent header to be set")
	}

	// Parse back
	parsed, ok := ParseHTTPHeaders(headers)
	if !ok {
		t.Fatal("Failed to parse headers")
	}

	if parsed.TraceID != tc.TraceID {
		t.Error("Parsed trace ID doesn't match original")
	}

	if parsed.Sampled != tc.Sampled {
		t.Error("Parsed sampled flag doesn't match original")
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
