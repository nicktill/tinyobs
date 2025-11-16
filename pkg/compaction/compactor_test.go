package compaction

import (
	"context"
	"testing"
	"time"

	"tinyobs/pkg/sdk/metrics"
	"tinyobs/pkg/storage"
	"tinyobs/pkg/storage/memory"
)

func TestCompact5m_BasicAggregation(t *testing.T) {
	store := memory.New()
	defer store.Close()

	compactor := New(store)
	ctx := context.Background()

	// Create raw metrics within same 5-minute bucket
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	rawMetrics := []metrics.Metric{
		{Name: "cpu", Value: 10, Timestamp: baseTime},
		{Name: "cpu", Value: 20, Timestamp: baseTime.Add(1 * time.Minute)},
		{Name: "cpu", Value: 30, Timestamp: baseTime.Add(2 * time.Minute)},
		{Name: "cpu", Value: 40, Timestamp: baseTime.Add(3 * time.Minute)},
	}

	store.Write(ctx, rawMetrics)

	// Compact
	err := compactor.Compact5m(ctx, baseTime.Add(-1*time.Hour), baseTime.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Verify aggregate was created
	// Should have: sum=100, count=4, min=10, max=40, avg=25
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: baseTime.Add(-1 * time.Hour),
		End:   baseTime.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Should have original 4 metrics + 1 aggregate
	if len(results) != 5 {
		t.Errorf("Expected 5 results (4 raw + 1 aggregate), got %d", len(results))
	}

	// Find the aggregate (it should have value = average = 25)
	foundAggregate := false
	for _, r := range results {
		if r.Value == 25 {
			foundAggregate = true
			break
		}
	}

	if !foundAggregate {
		t.Error("Expected to find aggregate with average value of 25")
	}
}

func TestCompact5m_MultipleSeries(t *testing.T) {
	store := memory.New()
	defer store.Close()

	compactor := New(store)
	ctx := context.Background()

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Two different series (different labels)
	rawMetrics := []metrics.Metric{
		{
			Name:      "requests",
			Value:     100,
			Labels:    map[string]string{"endpoint": "/api"},
			Timestamp: baseTime,
		},
		{
			Name:      "requests",
			Value:     200,
			Labels:    map[string]string{"endpoint": "/api"},
			Timestamp: baseTime.Add(1 * time.Minute),
		},
		{
			Name:      "requests",
			Value:     50,
			Labels:    map[string]string{"endpoint": "/health"},
			Timestamp: baseTime,
		},
	}

	store.Write(ctx, rawMetrics)

	err := compactor.Compact5m(ctx, baseTime.Add(-1*time.Hour), baseTime.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Should have 3 raw metrics + 2 aggregates (one per series)
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: baseTime.Add(-1 * time.Hour),
		End:   baseTime.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("Expected 5 results (3 raw + 2 aggregates), got %d", len(results))
	}
}

func TestCompact5m_AcrossBuckets(t *testing.T) {
	store := memory.New()
	defer store.Close()

	compactor := New(store)
	ctx := context.Background()

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Metrics spanning two 5-minute buckets
	rawMetrics := []metrics.Metric{
		{Name: "metric", Value: 10, Timestamp: baseTime},                       // 12:00-12:05 bucket
		{Name: "metric", Value: 20, Timestamp: baseTime.Add(3 * time.Minute)},  // 12:00-12:05 bucket
		{Name: "metric", Value: 30, Timestamp: baseTime.Add(6 * time.Minute)},  // 12:05-12:10 bucket
		{Name: "metric", Value: 40, Timestamp: baseTime.Add(8 * time.Minute)},  // 12:05-12:10 bucket
	}

	store.Write(ctx, rawMetrics)

	err := compactor.Compact5m(ctx, baseTime.Add(-1*time.Hour), baseTime.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Should have 4 raw + 2 aggregates (one per bucket)
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: baseTime.Add(-1 * time.Hour),
		End:   baseTime.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 6 {
		t.Errorf("Expected 6 results (4 raw + 2 aggregates), got %d", len(results))
	}
}

func TestCompact1h(t *testing.T) {
	store := memory.New()
	defer store.Close()

	compactor := New(store)
	ctx := context.Background()

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Simulate 5-minute aggregates across one hour
	fiveMinAggregates := []metrics.Metric{
		{Name: "metric", Value: 10, Timestamp: baseTime},                        // 12:00
		{Name: "metric", Value: 15, Timestamp: baseTime.Add(5 * time.Minute)},   // 12:05
		{Name: "metric", Value: 20, Timestamp: baseTime.Add(10 * time.Minute)},  // 12:10
		{Name: "metric", Value: 25, Timestamp: baseTime.Add(15 * time.Minute)},  // 12:15
		{Name: "metric", Value: 30, Timestamp: baseTime.Add(60 * time.Minute)},  // 13:00 (next hour)
	}

	store.Write(ctx, fiveMinAggregates)

	err := compactor.Compact1h(ctx, baseTime.Add(-1*time.Hour), baseTime.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Should have 5 input aggregates + 2 hourly aggregates (12:00 and 13:00 hours)
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: baseTime.Add(-1 * time.Hour),
		End:   baseTime.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 7 {
		t.Errorf("Expected 7 results (5 input + 2 hourly), got %d", len(results))
	}
}

func TestCompactAndCleanup(t *testing.T) {
	store := memory.New()
	defer store.Close()

	compactor := New(store)
	ctx := context.Background()

	now := time.Now()

	// Create old raw metrics (should be deleted)
	oldMetrics := []metrics.Metric{
		{Name: "old", Value: 1, Timestamp: now.Add(-10 * time.Hour)},
		{Name: "old", Value: 2, Timestamp: now.Add(-8 * time.Hour)},
	}

	// Create recent metrics (should be kept)
	recentMetrics := []metrics.Metric{
		{Name: "recent", Value: 10, Timestamp: now.Add(-1 * time.Hour)},
		{Name: "recent", Value: 20, Timestamp: now},
	}

	store.Write(ctx, oldMetrics)
	store.Write(ctx, recentMetrics)

	// Run cleanup
	err := compactor.CompactAndCleanup(ctx)
	if err != nil {
		t.Fatalf("CompactAndCleanup failed: %v", err)
	}

	// Query all remaining metrics
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-24 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Old metrics should be gone, recent ones should remain
	// (Plus any aggregates created during compaction)
	for _, r := range results {
		if r.Name == "old" {
			t.Error("Old metrics should have been deleted")
		}
	}
}

func TestRoundTo5Minutes(t *testing.T) {
	tests := []struct {
		input    time.Time
		expected time.Time
	}{
		{
			input:    time.Date(2024, 1, 1, 12, 0, 30, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			input:    time.Date(2024, 1, 1, 12, 3, 45, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			input:    time.Date(2024, 1, 1, 12, 7, 15, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 5, 0, 0, time.UTC),
		},
		{
			input:    time.Date(2024, 1, 1, 12, 14, 59, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 10, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		result := roundTo5Minutes(test.input)
		if !result.Equal(test.expected) {
			t.Errorf("roundTo5Minutes(%v) = %v, expected %v",
				test.input, result, test.expected)
		}
	}
}

func TestRoundTo1Hour(t *testing.T) {
	tests := []struct {
		input    time.Time
		expected time.Time
	}{
		{
			input:    time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			input:    time.Date(2024, 1, 1, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			input:    time.Date(2024, 1, 1, 12, 59, 59, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		result := roundTo1Hour(test.input)
		if !result.Equal(test.expected) {
			t.Errorf("roundTo1Hour(%v) = %v, expected %v",
				test.input, result, test.expected)
		}
	}
}

func TestCalculatePercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		percentile float64
		expected   float64
	}{
		{0.0, 1.0},   // min
		{0.5, 5.5},   // median
		{0.99, 9.91}, // p99
		{1.0, 10.0},  // max
	}

	for _, test := range tests {
		result := CalculatePercentile(values, test.percentile)
		if result < test.expected-0.1 || result > test.expected+0.1 {
			t.Errorf("CalculatePercentile(p%.2f) = %.2f, expected ~%.2f",
				test.percentile, result, test.expected)
		}
	}
}

func TestCalculatePercentile_EmptyValues(t *testing.T) {
	result := CalculatePercentile([]float64{}, 0.5)
	if result != 0 {
		t.Errorf("CalculatePercentile on empty slice should return 0, got %f", result)
	}
}
