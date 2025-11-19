package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Storage handles storing and querying distributed traces
type Storage struct {
	// In-memory storage for spans (keyed by trace ID)
	// Production system would use BadgerDB or similar
	traces map[TraceID][]*Span
	mu     sync.RWMutex

	// Retention policy
	maxTraces   int
	maxAge      time.Duration
	lastCleanup time.Time
}

// NewStorage creates a new trace storage
func NewStorage() *Storage {
	return &Storage{
		traces:      make(map[TraceID][]*Span),
		maxTraces:   10000,           // Keep up to 10k traces
		maxAge:      24 * time.Hour,  // Keep traces for 24 hours
		lastCleanup: time.Now(),
	}
}

// StoreSpan stores a span for a trace
func (s *Storage) StoreSpan(ctx context.Context, span *Span) error {
	if span == nil {
		return fmt.Errorf("span cannot be nil")
	}

	// Validate required fields
	if span.TraceID == "" {
		return fmt.Errorf("trace_id is required")
	}
	if span.SpanID == "" {
		return fmt.Errorf("span_id is required")
	}

	// Calculate duration if not set
	if span.Duration == 0 && !span.EndTime.IsZero() {
		span.Duration = span.EndTime.Sub(span.StartTime)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Add span to trace
	s.traces[span.TraceID] = append(s.traces[span.TraceID], span)

	// Periodic cleanup
	if time.Since(s.lastCleanup) > 5*time.Minute {
		go s.cleanup()
	}

	return nil
}

// GetTrace retrieves a complete trace by ID
func (s *Storage) GetTrace(ctx context.Context, traceID TraceID) (*Trace, error) {
	s.mu.RLock()
	spans := s.traces[traceID]
	s.mu.RUnlock()

	if len(spans) == 0 {
		return nil, fmt.Errorf("trace not found: %s", traceID)
	}

	// Build trace from spans
	trace := &Trace{
		TraceID: traceID,
		Spans:   make([]*Span, len(spans)),
	}

	// Copy spans (don't return internal pointers)
	copy(trace.Spans, spans)

	// Sort spans by start time
	sort.Slice(trace.Spans, func(i, j int) bool {
		return trace.Spans[i].StartTime.Before(trace.Spans[j].StartTime)
	})

	// Find root span (span with no parent)
	for _, span := range trace.Spans {
		if span.ParentID == "" {
			trace.RootSpan = span
		}
	}

	// Calculate trace metadata
	if len(trace.Spans) > 0 {
		trace.StartTime = trace.Spans[0].StartTime
		trace.EndTime = trace.Spans[0].EndTime

		servicesMap := make(map[string]bool)
		for _, span := range trace.Spans {
			if span.StartTime.Before(trace.StartTime) {
				trace.StartTime = span.StartTime
			}
			if span.EndTime.After(trace.EndTime) {
				trace.EndTime = span.EndTime
			}
			servicesMap[span.Service] = true
		}

		trace.Duration = trace.EndTime.Sub(trace.StartTime)

		// Extract unique services
		for service := range servicesMap {
			trace.Services = append(trace.Services, service)
		}
		sort.Strings(trace.Services)
	}

	return trace, nil
}

// QueryTraces retrieves traces within a time range
func (s *Storage) QueryTraces(ctx context.Context, start, end time.Time, limit int) ([]*Trace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var traceIDs []TraceID
	for traceID, spans := range s.traces {
		if len(spans) == 0 {
			continue
		}

		// Check if any span in this trace falls within the time range
		for _, span := range spans {
			if !span.StartTime.Before(start) && span.StartTime.Before(end) {
				traceIDs = append(traceIDs, traceID)
				break
			}
		}
	}

	// Sort by trace ID for consistent ordering
	sort.Slice(traceIDs, func(i, j int) bool {
		return traceIDs[i] < traceIDs[j]
	})

	// Apply limit
	if limit > 0 && len(traceIDs) > limit {
		traceIDs = traceIDs[:limit]
	}

	// Build traces
	var traces []*Trace
	for _, traceID := range traceIDs {
		trace, err := s.GetTrace(ctx, traceID)
		if err != nil {
			continue // Skip traces that error
		}
		traces = append(traces, trace)
	}

	return traces, nil
}

// GetRecentTraces returns the N most recent traces
func (s *Storage) GetRecentTraces(ctx context.Context, limit int) ([]*Trace, error) {
	end := time.Now()
	start := end.Add(-24 * time.Hour)
	return s.QueryTraces(ctx, start, end, limit)
}

// Stats returns storage statistics
func (s *Storage) Stats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totalSpans := 0
	for _, spans := range s.traces {
		totalSpans += len(spans)
	}

	return map[string]interface{}{
		"total_traces": len(s.traces),
		"total_spans":  totalSpans,
		"max_traces":   s.maxTraces,
		"max_age":      s.maxAge.String(),
	}
}

// cleanup removes old traces based on retention policy
func (s *Storage) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.lastCleanup = now

	// Remove traces older than maxAge
	for traceID, spans := range s.traces {
		if len(spans) == 0 {
			delete(s.traces, traceID)
			continue
		}

		// Check oldest span in trace
		oldestTime := spans[0].StartTime
		for _, span := range spans {
			if span.StartTime.Before(oldestTime) {
				oldestTime = span.StartTime
			}
		}

		if now.Sub(oldestTime) > s.maxAge {
			delete(s.traces, traceID)
		}
	}

	// If still over limit, remove oldest traces
	if len(s.traces) > s.maxTraces {
		type traceAge struct {
			id  TraceID
			age time.Time
		}

		var ages []traceAge
		for traceID, spans := range s.traces {
			if len(spans) > 0 {
				ages = append(ages, traceAge{
					id:  traceID,
					age: spans[0].StartTime,
				})
			}
		}

		// Sort by age (oldest first)
		sort.Slice(ages, func(i, j int) bool {
			return ages[i].age.Before(ages[j].age)
		})

		// Remove oldest traces
		toRemove := len(ages) - s.maxTraces
		for i := 0; i < toRemove; i++ {
			delete(s.traces, ages[i].id)
		}
	}
}

// Export exports all traces as JSON (for debugging/backup)
func (s *Storage) Export(ctx context.Context) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return json.MarshalIndent(s.traces, "", "  ")
}
