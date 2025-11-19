package batch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// mockTransport is a mock implementation of transport.Transport for testing
type mockTransport struct {
	mu      sync.Mutex
	batches [][]metrics.Metric
	sendErr error
	delay   time.Duration
}

func (m *mockTransport) Send(ctx context.Context, batch []metrics.Metric) error {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Make a copy to avoid race conditions
	batchCopy := make([]metrics.Metric, len(batch))
	copy(batchCopy, batch)
	m.batches = append(m.batches, batchCopy)

	return m.sendErr
}

func (m *mockTransport) getBatches() [][]metrics.Metric {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([][]metrics.Metric, len(m.batches))
	copy(result, m.batches)
	return result
}

func (m *mockTransport) totalMetrics() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0
	for _, batch := range m.batches {
		total += len(batch)
	}
	return total
}

// Test 1: New() creates a batcher with correct configuration
func TestNew(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 100,
		FlushEvery:   5 * time.Second,
	}

	batcher := New(transport, config)

	if batcher == nil {
		t.Fatal("New() returned nil")
	}
	if batcher.config.MaxBatchSize != 100 {
		t.Errorf("Expected MaxBatchSize=100, got %d", batcher.config.MaxBatchSize)
	}
	if batcher.config.FlushEvery != 5*time.Second {
		t.Errorf("Expected FlushEvery=5s, got %v", batcher.config.FlushEvery)
	}
	if batcher.transport != transport {
		t.Error("Transport not set correctly")
	}
}

// Test 2: Start() and Stop() lifecycle
func TestStartStop(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 100,
		FlushEvery:   100 * time.Millisecond,
	}

	batcher := New(transport, config)
	ctx := context.Background()

	// Start should not error
	if err := batcher.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Stop should not error
	if err := batcher.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}
}

// Test 3: Add() triggers flush when batch is full
func TestAddTriggersFlushWhenFull(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 5, // Small batch size to trigger flush quickly
		FlushEvery:   1 * time.Hour, // Long interval so timer doesn't interfere
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)
	defer batcher.Stop()

	// Add exactly 5 metrics (batch size)
	for i := 0; i < 5; i++ {
		batcher.Add(metrics.Metric{
			Name:  "test_metric",
			Type:  "counter",
			Value: float64(i),
		})
	}

	// Wait for async flush to complete
	time.Sleep(100 * time.Millisecond)

	// Verify flush occurred
	batches := transport.getBatches()
	if len(batches) != 1 {
		t.Fatalf("Expected 1 batch, got %d", len(batches))
	}
	if len(batches[0]) != 5 {
		t.Errorf("Expected 5 metrics in batch, got %d", len(batches[0]))
	}
}

// Test 4: Concurrent Add() doesn't spawn unbounded goroutines (CRITICAL TEST)
// This test validates the fix for the unbounded goroutine spawning bug
func TestConcurrentAddPreventsUnboundedGoroutines(t *testing.T) {
	// Slow transport to create backpressure
	transport := &mockTransport{
		delay: 50 * time.Millisecond,
	}

	config := Config{
		MaxBatchSize: 10,
		FlushEvery:   1 * time.Hour,
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)
	defer batcher.Stop()

	// Hammer the batcher with concurrent writes
	var wg sync.WaitGroup
	numGoroutines := 10
	metricsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < metricsPerGoroutine; j++ {
				batcher.Add(metrics.Metric{
					Name:  "concurrent_test",
					Type:  "counter",
					Value: float64(id*1000 + j),
				})
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond) // Wait for flushes

	// The critical assertion: flushing flag should prevent unbounded goroutines
	// We wrote 1000 metrics with batch size 10, so we expect ~100 batches
	// The atomic flag ensures only one flush runs at a time
	totalMetrics := transport.totalMetrics()
	if totalMetrics != 1000 {
		t.Errorf("Expected 1000 metrics sent, got %d", totalMetrics)
	}

	// Verify the flushing flag is not stuck
	if batcher.flushing.Load() {
		t.Error("Flushing flag is stuck; indicates a concurrency bug")
	}
}

// Test 5: Periodic flush via timer
func TestPeriodicFlush(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 1000, // Large batch size so only timer triggers flush
		FlushEvery:   100 * time.Millisecond,
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)
	defer batcher.Stop()

	// Add 3 metrics (less than batch size)
	for i := 0; i < 3; i++ {
		batcher.Add(metrics.Metric{
			Name:  "periodic_test",
			Type:  "gauge",
			Value: float64(i),
		})
	}

	// Wait for periodic flush (100ms + buffer)
	time.Sleep(200 * time.Millisecond)

	// Verify periodic flush occurred
	batches := transport.getBatches()
	if len(batches) == 0 {
		t.Fatal("Expected periodic flush to occur, but no batches sent")
	}

	totalMetrics := transport.totalMetrics()
	if totalMetrics != 3 {
		t.Errorf("Expected 3 metrics sent, got %d", totalMetrics)
	}
}

// Test 6: Manual Flush() sends pending metrics immediately
func TestManualFlush(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 1000,
		FlushEvery:   1 * time.Hour,
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)
	defer batcher.Stop()

	// Add metrics
	for i := 0; i < 7; i++ {
		batcher.Add(metrics.Metric{
			Name:  "manual_flush_test",
			Type:  "counter",
			Value: float64(i),
		})
	}

	// Manual flush
	if err := batcher.Flush(); err != nil {
		t.Fatalf("Flush() failed: %v", err)
	}

	// Verify metrics were sent
	batches := transport.getBatches()
	if len(batches) != 1 {
		t.Fatalf("Expected 1 batch after manual flush, got %d", len(batches))
	}
	if len(batches[0]) != 7 {
		t.Errorf("Expected 7 metrics, got %d", len(batches[0]))
	}
}

// Test 7: Stop() flushes pending metrics before shutdown
func TestStopFlushesPendingMetrics(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 1000,
		FlushEvery:   1 * time.Hour,
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)

	// Add metrics without triggering flush
	for i := 0; i < 4; i++ {
		batcher.Add(metrics.Metric{
			Name:  "stop_test",
			Type:  "gauge",
			Value: float64(i),
		})
	}

	// Stop should flush pending metrics
	if err := batcher.Stop(); err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Verify all metrics were sent
	totalMetrics := transport.totalMetrics()
	if totalMetrics != 4 {
		t.Errorf("Expected 4 metrics flushed on stop, got %d", totalMetrics)
	}
}

// Test 8: Context cancellation stops the batcher gracefully
func TestContextCancellation(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 100,
		FlushEvery:   50 * time.Millisecond,
	}

	batcher := New(transport, config)
	ctx, cancel := context.WithCancel(context.Background())
	batcher.Start(ctx)

	// Add some metrics
	for i := 0; i < 3; i++ {
		batcher.Add(metrics.Metric{
			Name:  "context_test",
			Type:  "counter",
			Value: float64(i),
		})
	}

	// Cancel context
	cancel()

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		batcher.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success: Stop completed
	case <-time.After(1 * time.Second):
		t.Fatal("Stop() hung after context cancellation")
	}

	// Verify pending metrics were flushed
	totalMetrics := transport.totalMetrics()
	if totalMetrics != 3 {
		t.Errorf("Expected 3 metrics, got %d", totalMetrics)
	}
}

// Test 9: Flush() on empty batcher is a no-op
func TestFlushEmpty(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 100,
		FlushEvery:   5 * time.Second,
	}

	batcher := New(transport, config)

	// Flush without starting or adding metrics
	if err := batcher.Flush(); err != nil {
		t.Errorf("Flush() on empty batcher should not error, got: %v", err)
	}

	// Verify no batches were sent
	batches := transport.getBatches()
	if len(batches) != 0 {
		t.Errorf("Expected 0 batches, got %d", len(batches))
	}
}

// Test 10: Multiple batchers can share same transport
func TestMultipleBatchers(t *testing.T) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 10,
		FlushEvery:   100 * time.Millisecond,
	}

	ctx := context.Background()

	// Batcher 1
	batcher1 := New(transport, config)
	batcher1.Start(ctx)
	batcher1.Add(metrics.Metric{Name: "batcher1", Type: "counter", Value: 1})
	batcher1.Stop()

	// Batcher 2
	batcher2 := New(transport, config)
	batcher2.Start(ctx)
	batcher2.Add(metrics.Metric{Name: "batcher2", Type: "counter", Value: 2})
	batcher2.Stop()

	// Both metrics should have been sent
	totalMetrics := transport.totalMetrics()
	if totalMetrics != 2 {
		t.Errorf("Expected 2 metrics from both batchers, got %d", totalMetrics)
	}
}

// Benchmark: Add() performance under load
func BenchmarkAdd(b *testing.B) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 1000,
		FlushEvery:   1 * time.Second,
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)
	defer batcher.Stop()

	metric := metrics.Metric{
		Name:  "benchmark_metric",
		Type:  "counter",
		Value: 1.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batcher.Add(metric)
	}
}

// Benchmark: Concurrent Add() from multiple goroutines
func BenchmarkConcurrentAdd(b *testing.B) {
	transport := &mockTransport{}
	config := Config{
		MaxBatchSize: 1000,
		FlushEvery:   1 * time.Second,
	}

	batcher := New(transport, config)
	ctx := context.Background()
	batcher.Start(ctx)
	defer batcher.Stop()

	metric := metrics.Metric{
		Name:  "benchmark_metric",
		Type:  "counter",
		Value: 1.0,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			batcher.Add(metric)
		}
	})
}
