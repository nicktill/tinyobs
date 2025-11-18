package query

import (
	"context"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// MockStorage implements storage.Storage for testing
type MockStorage struct {
	metrics []metrics.Metric
}

func (m *MockStorage) Write(ctx context.Context, metrics []metrics.Metric) error {
	m.metrics = append(m.metrics, metrics...)
	return nil
}

func (m *MockStorage) Query(ctx context.Context, req storage.QueryRequest) ([]metrics.Metric, error) {
	// Return all metrics for simplicity
	return m.metrics, nil
}

func (m *MockStorage) Delete(ctx context.Context, opts storage.DeleteOptions) error {
	return nil
}

func (m *MockStorage) Close() error {
	return nil
}

func (m *MockStorage) Stats(ctx context.Context) (*storage.Stats, error) {
	return &storage.Stats{}, nil
}

func TestMemoryLimit(t *testing.T) {
	// Create executor with low sample limit
	store := &MockStorage{}
	executor := NewExecutorWithConfig(store, ExecutorConfig{
		MaxSamples: 100, // Very low limit for testing
	})

	// Add 150 samples to storage (exceeds limit)
	now := time.Now()
	testMetrics := make([]metrics.Metric, 150)
	for i := 0; i < 150; i++ {
		testMetrics[i] = metrics.Metric{
			Name:      "test_metric",
			Type:      "counter",
			Value:     float64(i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Labels:    map[string]string{"instance": "test"},
		}
	}
	store.Write(context.Background(), testMetrics)

	// Parse query
	parser := NewParser("test_metric")
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Execute query - should fail with memory limit
	query := &Query{
		Expr:  expr,
		Start: now,
		End:   now.Add(200 * time.Second),
		Step:  time.Second,
	}

	result, err := executor.Execute(context.Background(), query)
	if err == nil {
		result.Close()
		t.Fatal("Expected error for exceeding sample limit, got nil")
	}

	if err.Error() != "query exceeded max samples limit: loaded 150, limit 100 (reduce time range or increase MaxSamples)" {
		t.Errorf("Expected sample limit error, got: %v", err)
	}
}

func TestResultClose(t *testing.T) {
	// Create result with data
	result := &Result{
		Series: []TimeSeries{
			{
				Labels: map[string]string{"foo": "bar"},
				Points: []Point{
					{Time: time.Now(), Value: 1.0},
					{Time: time.Now(), Value: 2.0},
				},
			},
		},
		TotalSamples: 2,
	}

	// Close should clear all data
	err := result.Close()
	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Verify everything is nil
	if result.Series != nil {
		t.Error("Series not nil after Close()")
	}
	if result.TotalSamples != 0 {
		t.Error("TotalSamples not zero after Close()")
	}
}

func TestResultCloseNil(t *testing.T) {
	// Close on nil result should not panic
	var result *Result
	err := result.Close()
	if err != nil {
		t.Fatalf("Close() on nil result returned error: %v", err)
	}
}

func TestMemoryLimitWithinBounds(t *testing.T) {
	// Create executor with sufficient limit
	store := &MockStorage{}
	executor := NewExecutorWithConfig(store, ExecutorConfig{
		MaxSamples: 1000,
	})

	// Add 50 samples (well under limit)
	now := time.Now()
	testMetrics := make([]metrics.Metric, 50)
	for i := 0; i < 50; i++ {
		testMetrics[i] = metrics.Metric{
			Name:      "test_metric",
			Type:      "counter",
			Value:     float64(i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Labels:    map[string]string{"instance": "test"},
		}
	}
	store.Write(context.Background(), testMetrics)

	// Parse query
	parser := NewParser("test_metric")
	expr, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Execute query - should succeed
	query := &Query{
		Expr:  expr,
		Start: now,
		End:   now.Add(100 * time.Second),
		Step:  time.Second,
	}

	result, err := executor.Execute(context.Background(), query)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	defer result.Close()

	if result.TotalSamples != 50 {
		t.Errorf("Expected 50 samples, got %d", result.TotalSamples)
	}
}

func TestDefaultExecutorConfig(t *testing.T) {
	config := DefaultExecutorConfig()
	if config.MaxSamples != 10_000_000 {
		t.Errorf("Expected default MaxSamples to be 10M, got %d", config.MaxSamples)
	}
}
