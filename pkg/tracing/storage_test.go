package tracing

import (
	"context"
	"testing"
	"time"
)

func TestNewStorage(t *testing.T) {
	storage := NewStorage()

	if storage == nil {
		t.Fatal("Expected storage to be created")
	}

	if storage.maxTraces != 10000 {
		t.Errorf("Expected maxTraces to be 10000, got %d", storage.maxTraces)
	}

	if storage.maxAge != 24*time.Hour {
		t.Errorf("Expected maxAge to be 24h, got %v", storage.maxAge)
	}
}

func TestStoreSpan(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	span := &Span{
		TraceID:   TraceID("test-trace-123"),
		SpanID:    SpanID("test-span-456"),
		Service:   "test-service",
		Operation: "test-op",
		StartTime: time.Now(),
		EndTime:   time.Now().Add(100 * time.Millisecond),
		Kind:      SpanKindServer,
		Status:    SpanStatusOK,
	}

	err := storage.StoreSpan(ctx, span)
	if err != nil {
		t.Fatalf("Failed to store span: %v", err)
	}

	// Verify span was stored
	trace, err := storage.GetTrace(ctx, span.TraceID)
	if err != nil {
		t.Fatalf("Failed to get trace: %v", err)
	}

	if len(trace.Spans) != 1 {
		t.Errorf("Expected 1 span, got %d", len(trace.Spans))
	}

	if trace.Spans[0].SpanID != span.SpanID {
		t.Error("Span IDs don't match")
	}
}

func TestStoreMultipleSpans(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	traceID := TraceID("test-trace-multi")

	// Store multiple spans for same trace
	for i := 0; i < 5; i++ {
		span := &Span{
			TraceID:   traceID,
			SpanID:    SpanID(string(rune('A' + i))),
			Service:   "test-service",
			Operation: "test-op",
			StartTime: time.Now(),
			EndTime:   time.Now().Add(time.Duration(i*10) * time.Millisecond),
			Kind:      SpanKindInternal,
			Status:    SpanStatusOK,
		}

		err := storage.StoreSpan(ctx, span)
		if err != nil {
			t.Fatalf("Failed to store span %d: %v", i, err)
		}
	}

	// Verify all spans were stored
	trace, err := storage.GetTrace(ctx, traceID)
	if err != nil {
		t.Fatalf("Failed to get trace: %v", err)
	}

	if len(trace.Spans) != 5 {
		t.Errorf("Expected 5 spans, got %d", len(trace.Spans))
	}
}

func TestGetTraceNotFound(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	_, err := storage.GetTrace(ctx, TraceID("nonexistent"))
	if err == nil {
		t.Error("Expected error for nonexistent trace")
	}
}

func TestQueryTraces(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	now := time.Now()

	// Store spans at different times
	for i := 0; i < 3; i++ {
		span := &Span{
			TraceID:   TraceID(string(rune('X' + i))),
			SpanID:    SpanID("span1"),
			Service:   "test-service",
			Operation: "test-op",
			StartTime: now.Add(time.Duration(i) * time.Hour),
			EndTime:   now.Add(time.Duration(i)*time.Hour + 100*time.Millisecond),
			Kind:      SpanKindServer,
			Status:    SpanStatusOK,
		}
		storage.StoreSpan(ctx, span)
	}

	// Query for traces in time range
	start := now.Add(-1 * time.Hour)
	end := now.Add(2 * time.Hour)

	traces, err := storage.QueryTraces(ctx, start, end, 10)
	if err != nil {
		t.Fatalf("Failed to query traces: %v", err)
	}

	if len(traces) != 2 {
		t.Errorf("Expected 2 traces in range, got %d", len(traces))
	}
}

func TestGetRecentTraces(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	// Store some recent spans
	for i := 0; i < 5; i++ {
		span := &Span{
			TraceID:   TraceID(string(rune('A' + i))),
			SpanID:    SpanID("span1"),
			Service:   "test-service",
			Operation: "test-op",
			StartTime: time.Now(),
			EndTime:   time.Now().Add(100 * time.Millisecond),
			Kind:      SpanKindServer,
			Status:    SpanStatusOK,
		}
		storage.StoreSpan(ctx, span)
		time.Sleep(1 * time.Millisecond) // Ensure different timestamps
	}

	traces, err := storage.GetRecentTraces(ctx, 3)
	if err != nil {
		t.Fatalf("Failed to get recent traces: %v", err)
	}

	if len(traces) != 3 {
		t.Errorf("Expected 3 traces, got %d", len(traces))
	}
}

func TestStorageStats(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	// Store some spans
	for i := 0; i < 3; i++ {
		for j := 0; j < 2; j++ {
			span := &Span{
				TraceID:   TraceID(string(rune('A' + i))),
				SpanID:    SpanID(string(rune('X' + j))),
				Service:   "test-service",
				Operation: "test-op",
				StartTime: time.Now(),
				EndTime:   time.Now().Add(100 * time.Millisecond),
				Kind:      SpanKindServer,
				Status:    SpanStatusOK,
			}
			storage.StoreSpan(ctx, span)
		}
	}

	stats := storage.Stats()

	if stats.TotalTraces != 3 {
		t.Errorf("Expected 3 traces in stats, got %d", stats.TotalTraces)
	}

	if stats.TotalSpans != 6 {
		t.Errorf("Expected 6 spans in stats, got %d", stats.TotalSpans)
	}
}

func TestTraceMetadata(t *testing.T) {
	storage := NewStorage()
	ctx := context.Background()

	traceID := TraceID("test-trace-metadata")
	now := time.Now()

	// Create root span
	rootSpan := &Span{
		TraceID:   traceID,
		SpanID:    SpanID("root"),
		ParentID:  "",
		Service:   "api-service",
		Operation: "GET /api/users",
		StartTime: now,
		EndTime:   now.Add(100 * time.Millisecond),
		Kind:      SpanKindServer,
		Status:    SpanStatusOK,
	}
	storage.StoreSpan(ctx, rootSpan)

	// Create child span
	childSpan := &Span{
		TraceID:   traceID,
		SpanID:    SpanID("child"),
		ParentID:  SpanID("root"),
		Service:   "db-service",
		Operation: "query",
		StartTime: now.Add(10 * time.Millisecond),
		EndTime:   now.Add(80 * time.Millisecond),
		Kind:      SpanKindInternal,
		Status:    SpanStatusOK,
	}
	storage.StoreSpan(ctx, childSpan)

	// Get trace and verify metadata
	trace, err := storage.GetTrace(ctx, traceID)
	if err != nil {
		t.Fatalf("Failed to get trace: %v", err)
	}

	if trace.RootSpan == nil {
		t.Fatal("Expected root span to be identified")
	}

	if trace.RootSpan.SpanID != SpanID("root") {
		t.Error("Wrong root span identified")
	}

	if len(trace.Services) != 2 {
		t.Errorf("Expected 2 services, got %d", len(trace.Services))
	}

	if trace.Duration == 0 {
		t.Error("Expected trace duration to be calculated")
	}
}
