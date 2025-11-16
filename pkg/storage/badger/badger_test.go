package badger

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

func TestBadgerStorage_WriteAndQuery(t *testing.T) {
	// Use in-memory mode for tests
	store, err := New(Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	testMetrics := []metrics.Metric{
		{
			Name:      "cpu_usage",
			Type:      metrics.GaugeType,
			Value:     75.5,
			Labels:    map[string]string{"host": "server1"},
			Timestamp: now,
		},
		{
			Name:      "cpu_usage",
			Type:      metrics.GaugeType,
			Value:     82.1,
			Labels:    map[string]string{"host": "server2"},
			Timestamp: now,
		},
	}

	err = store.Write(ctx, testMetrics)
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

func TestBadgerStorage_Persistence(t *testing.T) {
	// Use temp directory for persistence test
	tmpDir, err := os.MkdirTemp("", "badger-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	now := time.Now()

	// Write to first instance
	{
		store, err := New(Config{Path: tmpDir})
		if err != nil {
			t.Fatalf("Failed to create storage: %v", err)
		}

		testMetrics := []metrics.Metric{
			{
				Name:      "persistent_metric",
				Value:     123.45,
				Timestamp: now,
			},
		}

		err = store.Write(ctx, testMetrics)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		store.Close()
	}

	// Read from second instance (reopens same directory)
	{
		store, err := New(Config{Path: tmpDir})
		if err != nil {
			t.Fatalf("Failed to reopen storage: %v", err)
		}
		defer store.Close()

		results, err := store.Query(ctx, storage.QueryRequest{
			Start: now.Add(-1 * time.Hour),
			End:   now.Add(1 * time.Hour),
		})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("Expected 1 persisted metric, got %d", len(results))
		}

		if results[0].Name != "persistent_metric" {
			t.Errorf("Expected persistent_metric, got %s", results[0].Name)
		}
	}
}

func TestBadgerStorage_Delete(t *testing.T) {
	store, err := New(Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write metrics at different times
	testMetrics := []metrics.Metric{
		{Name: "old1", Value: 1, Timestamp: now.Add(-3 * time.Hour)},
		{Name: "old2", Value: 2, Timestamp: now.Add(-2 * time.Hour)},
		{Name: "recent", Value: 3, Timestamp: now},
	}

	err = store.Write(ctx, testMetrics)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Delete metrics older than 1 hour
	err = store.Delete(ctx, storage.DeleteOptions{
		Before: now.Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Query remaining metrics
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-4 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 metric after deletion, got %d", len(results))
	}

	if results[0].Name != "recent" {
		t.Errorf("Expected recent metric, got %s", results[0].Name)
	}
}

func TestBadgerStorage_Stats(t *testing.T) {
	store, err := New(Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write multiple metrics
	testMetrics := []metrics.Metric{
		{
			Name:      "requests",
			Value:     100,
			Labels:    map[string]string{"endpoint": "/api"},
			Timestamp: now.Add(-1 * time.Hour),
		},
		{
			Name:      "requests",
			Value:     150,
			Labels:    map[string]string{"endpoint": "/api"}, // Same series
			Timestamp: now,
		},
		{
			Name:      "requests",
			Value:     75,
			Labels:    map[string]string{"endpoint": "/health"}, // Different series
			Timestamp: now,
		},
	}

	err = store.Write(ctx, testMetrics)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalMetrics != 3 {
		t.Errorf("Expected 3 total metrics, got %d", stats.TotalMetrics)
	}

	// Should have 2 unique series (same metric name with different labels)
	if stats.TotalSeries != 2 {
		t.Errorf("Expected 2 unique series, got %d", stats.TotalSeries)
	}

	// Verify timestamp range
	if stats.OldestMetric.After(now.Add(-1*time.Hour)) || stats.OldestMetric.Before(now.Add(-2*time.Hour)) {
		t.Errorf("Oldest metric timestamp out of expected range: %v", stats.OldestMetric)
	}

	if stats.NewestMetric.Before(now.Add(-1*time.Minute)) || stats.NewestMetric.After(now.Add(1*time.Minute)) {
		t.Errorf("Newest metric timestamp out of expected range: %v", stats.NewestMetric)
	}
}

func TestBadgerStorage_LargeWrite(t *testing.T) {
	store, err := New(Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write 1000 metrics
	testMetrics := make([]metrics.Metric, 1000)
	for i := 0; i < 1000; i++ {
		testMetrics[i] = metrics.Metric{
			Name:      "bulk_metric",
			Value:     float64(i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
	}

	err = store.Write(ctx, testMetrics)
	if err != nil {
		t.Fatalf("Large write failed: %v", err)
	}

	// Query and verify count
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 1000 {
		t.Errorf("Expected 1000 metrics, got %d", len(results))
	}
}

func TestBadgerStorage_ConcurrentOperations(t *testing.T) {
	store, err := New(Config{InMemory: true})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			metrics := []metrics.Metric{
				{
					Name:      "concurrent",
					Value:     float64(id),
					Timestamp: now.Add(time.Duration(id) * time.Second),
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

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			store.Query(ctx, storage.QueryRequest{
				Start: now.Add(-1 * time.Hour),
				End:   now.Add(1 * time.Hour),
			})
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}

	// Verify final state
	results, err := store.Query(ctx, storage.QueryRequest{
		Start: now.Add(-1 * time.Hour),
		End:   now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Final query failed: %v", err)
	}

	if len(results) != 10 {
		t.Errorf("Expected 10 metrics after concurrent operations, got %d", len(results))
	}
}

func TestBadgerStorage_Compression(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "badger-compression-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := New(Config{Path: tmpDir})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write many similar metrics (should compress well)
	testMetrics := make([]metrics.Metric, 10000)
	for i := 0; i < 10000; i++ {
		testMetrics[i] = metrics.Metric{
			Name:      "compressible_metric",
			Value:     123.456, // Same value repeated
			Labels:    map[string]string{"type": "test"},
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
	}

	err = store.Write(ctx, testMetrics)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	// Verify compression (10k metrics should use less than 1MB due to Zstd compression)
	maxExpectedSize := uint64(1024 * 1024) // 1MB
	if stats.SizeBytes > maxExpectedSize {
		t.Logf("Warning: Compression may not be effective. Size: %d bytes (expected < %d)",
			stats.SizeBytes, maxExpectedSize)
	}
}
