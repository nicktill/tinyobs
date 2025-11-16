package ingest

import (
	"testing"
	"time"

	"tinyobs/pkg/sdk/metrics"
)

func TestValidateMetric(t *testing.T) {
	tests := []struct {
		name    string
		metric  metrics.Metric
		wantErr bool
		errType error
	}{
		{
			name: "valid metric",
			metric: metrics.Metric{
				Name:  "cpu_usage",
				Value: 75.5,
				Labels: map[string]string{
					"host": "server1",
				},
				Timestamp: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "empty metric name",
			metric: metrics.Metric{
				Name:      "",
				Value:     1.0,
				Timestamp: time.Now(),
			},
			wantErr: true,
			errType: ErrMetricNameEmpty,
		},
		{
			name: "metric name too long",
			metric: metrics.Metric{
				Name:      string(make([]byte, MaxMetricNameLength+1)),
				Value:     1.0,
				Timestamp: time.Now(),
			},
			wantErr: true,
			errType: ErrMetricNameTooLong,
		},
		{
			name: "too many labels",
			metric: metrics.Metric{
				Name:      "test",
				Value:     1.0,
				Labels:    generateLabels(MaxLabelsPerMetric + 1),
				Timestamp: time.Now(),
			},
			wantErr: true,
			errType: ErrTooManyLabels,
		},
		{
			name: "label key too long",
			metric: metrics.Metric{
				Name:  "test",
				Value: 1.0,
				Labels: map[string]string{
					string(make([]byte, MaxLabelKeyLength+1)): "value",
				},
				Timestamp: time.Now(),
			},
			wantErr: true,
			errType: ErrLabelKeyTooLong,
		},
		{
			name: "label value too long",
			metric: metrics.Metric{
				Name:  "test",
				Value: 1.0,
				Labels: map[string]string{
					"key": string(make([]byte, MaxLabelValueLength+1)),
				},
				Timestamp: time.Now(),
			},
			wantErr: true,
			errType: ErrLabelValueTooLong,
		},
		{
			name: "max valid labels",
			metric: metrics.Metric{
				Name:      "test",
				Value:     1.0,
				Labels:    generateLabels(MaxLabelsPerMetric),
				Timestamp: time.Now(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetric(tt.metric)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMetric() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCardinalityTracker(t *testing.T) {
	tracker := NewCardinalityTracker()

	// Test new metric is accepted
	m1 := metrics.Metric{
		Name:      "cpu_usage",
		Value:     75.5,
		Labels:    map[string]string{"host": "server1"},
		Timestamp: time.Now(),
	}

	if err := tracker.Check(m1); err != nil {
		t.Errorf("Check() failed for new metric: %v", err)
	}

	tracker.Record(m1)

	// Test same metric is still accepted (already seen)
	if err := tracker.Check(m1); err != nil {
		t.Errorf("Check() failed for existing metric: %v", err)
	}

	// Test different labels creates new series
	m2 := metrics.Metric{
		Name:      "cpu_usage",
		Value:     82.1,
		Labels:    map[string]string{"host": "server2"},
		Timestamp: time.Now(),
	}

	if err := tracker.Check(m2); err != nil {
		t.Errorf("Check() failed for new series: %v", err)
	}

	tracker.Record(m2)

	// Verify stats
	stats := tracker.Stats()
	if stats.TotalSeries != 2 {
		t.Errorf("Expected 2 total series, got %d", stats.TotalSeries)
	}
	if stats.UniqueMetrics != 1 {
		t.Errorf("Expected 1 unique metric, got %d", stats.UniqueMetrics)
	}
}

func TestCardinalityTracker_PerMetricLimit(t *testing.T) {
	tracker := NewCardinalityTracker()

	// Add metrics up to the per-metric limit
	for i := 0; i < MaxSeriesPerMetric; i++ {
		m := metrics.Metric{
			Name:      "test_metric",
			Value:     float64(i),
			Labels:    map[string]string{"id": string(rune(i))},
			Timestamp: time.Now(),
		}
		if err := tracker.Check(m); err != nil {
			t.Fatalf("Check() failed at %d/%d: %v", i, MaxSeriesPerMetric, err)
		}
		tracker.Record(m)
	}

	// Next metric for same name should fail
	m := metrics.Metric{
		Name:      "test_metric",
		Value:     1.0,
		Labels:    map[string]string{"id": "new"},
		Timestamp: time.Now(),
	}

	err := tracker.Check(m)
	if err != ErrMetricCardinalityLimit {
		t.Errorf("Expected ErrMetricCardinalityLimit, got %v", err)
	}

	// Different metric name should still work
	m2 := metrics.Metric{
		Name:      "other_metric",
		Value:     1.0,
		Labels:    map[string]string{"id": "1"},
		Timestamp: time.Now(),
	}

	if err := tracker.Check(m2); err != nil {
		t.Errorf("Check() failed for different metric: %v", err)
	}
}

func TestCardinalityTracker_IgnoresInternalLabels(t *testing.T) {
	tracker := NewCardinalityTracker()

	// Metrics with same user labels but different internal labels should be same series
	m1 := metrics.Metric{
		Name:  "test",
		Value: 1.0,
		Labels: map[string]string{
			"host":           "server1",
			"__resolution__": "5m", // Internal label
		},
		Timestamp: time.Now(),
	}

	m2 := metrics.Metric{
		Name:  "test",
		Value: 2.0,
		Labels: map[string]string{
			"host":           "server1",
			"__resolution__": "1h", // Different internal label
		},
		Timestamp: time.Now(),
	}

	tracker.Record(m1)
	tracker.Record(m2)

	stats := tracker.Stats()
	if stats.TotalSeries != 1 {
		t.Errorf("Internal labels should be ignored, expected 1 series, got %d", stats.TotalSeries)
	}
}

// Helper function to generate N labels
func generateLabels(n int) map[string]string {
	labels := make(map[string]string, n)
	for i := 0; i < n; i++ {
		labels[string(rune('a'+i))] = "value"
	}
	return labels
}
