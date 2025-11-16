package memory

import (
	"context"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

func TestMemoryStorage_WriteAndQuery(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write some metrics
	testMetrics := []metrics.Metric{
		{
			Name:      "http_requests_total",
			Type:      metrics.CounterType,
			Value:     42,
			Labels:    map[string]string{"endpoint": "/api", "method": "GET"},
			Timestamp: now,
		},
		{
			Name:      "http_requests_total",
			Type:      metrics.CounterType,
			Value:     13,
			Labels:    map[string]string{"endpoint": "/health", "method": "GET"},
			Timestamp: now,
		},
	}

	err := store.Write(ctx, testMetrics)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Query all metrics
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 metrics, got %d", len(results))
	}
}

func TestMemoryStorage_QueryWithFilters(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write metrics with different names and labels
	testMetrics := []metrics.Metric{
		{
			Name:      "http_requests_total",
			Value:     1,
			Labels:    map[string]string{"endpoint": "/api"},
			Timestamp: now,
		},
		{
			Name:      "http_requests_total",
			Value:     2,
			Labels:    map[string]string{"endpoint": "/health"},
			Timestamp: now,
		},
		{
			Name:      "memory_usage_bytes",
			Value:     1024,
			Labels:    map[string]string{"host": "server1"},
			Timestamp: now,
		},
	}

	store.Write(ctx, testMetrics)

	// Filter by metric name
	results, err := store.Query(ctx, storage.QueryRequest{
		Start:       now.Add(-1 * time.Hour),
		End:         now.Add(1 * time.Hour),
		MetricNames: []string{"http_requests_total"},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 http_requests_total metrics, got %d", len(results))
	}

	// Filter by labels
	results, err = store.Query(ctx, storage.QueryRequest{
		Start:  now.Add(-1 * time.Hour),
		End:    now.Add(1 * time.Hour),
		Labels: map[string]string{"endpoint": "/api"},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 metric with endpoint=/api, got %d", len(results))
	}
	if results[0].Labels["endpoint"] != "/api" {
		t.Errorf("Expected endpoint=/api, got %s", results[0].Labels["endpoint"])
	}
}

func TestMemoryStorage_QueryTimeRange(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write metrics at different times
	testMetrics := []metrics.Metric{
		{Name: "metric1", Value: 1, Timestamp: now.Add(-2 * time.Hour)},
		{Name: "metric2", Value: 2, Timestamp: now.Add(-1 * time.Hour)},
		{Name: "metric3", Value: 3, Timestamp: now},
		{Name: "metric4", Value: 4, Timestamp: now.Add(1 * time.Hour)},
	}

	store.Write(ctx, testMetrics)

	// Query only last hour
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Should get metric2 and metric3 (not metric1 or metric4)
	if len(results) != 2 {
		t.Errorf("Expected 2 metrics in time range, got %d", len(results))
	}
}

func TestMemoryStorage_QueryLimit(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write 10 metrics
	testMetrics := make([]metrics.Metric, 10)
	for i := 0; i < 10; i++ {
		testMetrics[i] = metrics.Metric{
			Name:      "test_metric",
			Value:     float64(i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
	}

	store.Write(ctx, testMetrics)

	// Query with limit
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(1 * time.Hour),
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("Expected limit of 5 metrics, got %d", len(results))
	}
}

func TestMemoryStorage_Delete(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write metrics at different times
	testMetrics := []metrics.Metric{
		{Name: "old_metric", Value: 1, Timestamp: now.Add(-2 * time.Hour)},
		{Name: "recent_metric", Value: 2, Timestamp: now},
	}

	store.Write(ctx, testMetrics)

	// Delete metrics older than 1 hour
	err := store.Delete(ctx, storage.DeleteOptions{
		Before: now.Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Query all remaining metrics
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-3 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Should only have recent_metric
	if len(results) != 1 {
		t.Errorf("Expected 1 metric after deletion, got %d", len(results))
	}
	if results[0].Name != "recent_metric" {
		t.Errorf("Expected recent_metric, got %s", results[0].Name)
	}
}

func TestMemoryStorage_Stats(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write metrics with different series
	testMetrics := []metrics.Metric{
		{
			Name:      "http_requests",
			Value:     1,
			Labels:    map[string]string{"endpoint": "/api"},
			Timestamp: now.Add(-1 * time.Hour),
		},
		{
			Name:      "http_requests",
			Value:     2,
			Labels:    map[string]string{"endpoint": "/api"}, // Same series
			Timestamp: now,
		},
		{
			Name:      "http_requests",
			Value:     3,
			Labels:    map[string]string{"endpoint": "/health"}, // Different series
			Timestamp: now,
		},
		{
			Name:      "memory_usage",
			Value:     1024,
			Timestamp: now, // Different metric name = different series
		},
	}

	store.Write(ctx, testMetrics)

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalMetrics != 4 {
		t.Errorf("Expected 4 total metrics, got %d", stats.TotalMetrics)
	}

	// 3 unique series: http_requests{endpoint=/api}, http_requests{endpoint=/health}, memory_usage
	if stats.TotalSeries != 3 {
		t.Errorf("Expected 3 unique series, got %d", stats.TotalSeries)
	}

	// Check timestamp range
	expectedOldest := now.Add(-1 * time.Hour)
	if !stats.OldestMetric.Equal(expectedOldest) {
		t.Errorf("Expected oldest metric at %v, got %v", expectedOldest, stats.OldestMetric)
	}

	if !stats.NewestMetric.Equal(now) {
		t.Errorf("Expected newest metric at %v, got %v", now, stats.NewestMetric)
	}
}

func TestMemoryStorage_ConcurrentWrites(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			metrics := []metrics.Metric{
				{
					Name:      "concurrent_metric",
					Value:     float64(id),
					Timestamp: now,
				},
			}
			store.Write(ctx, metrics)
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all writes succeeded
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 10 {
		t.Errorf("Expected 10 metrics from concurrent writes, got %d", len(results))
	}
}

func TestMemoryStorage_EmptyQuery(t *testing.T) {
	store := New()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Query empty storage
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 metrics from empty storage, got %d", len(results))
	}

	// Stats on empty storage
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalMetrics != 0 {
		t.Errorf("Expected 0 total metrics, got %d", stats.TotalMetrics)
	}
}
