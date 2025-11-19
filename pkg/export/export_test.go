package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage/memory"
)

func TestExportToJSON(t *testing.T) {
	// Create in-memory storage
	store := memory.New()
	defer store.Close()

	// Write test metrics
	ctx := context.Background()
	testMetrics := []metrics.Metric{
		{
			Name:      "test_counter",
			Type:      metrics.CounterType,
			Value:     10,
			Labels:    map[string]string{"service": "test"},
			Timestamp: time.Now(),
		},
		{
			Name:      "test_gauge",
			Type:      metrics.GaugeType,
			Value:     42.5,
			Labels:    map[string]string{"service": "test", "host": "localhost"},
			Timestamp: time.Now(),
		},
	}

	if err := store.Write(ctx, testMetrics); err != nil {
		t.Fatalf("Failed to write test metrics: %v", err)
	}

	// Export to JSON
	exporter := NewExporter(store)
	buf := &bytes.Buffer{}
	opts := ExportOptions{
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now().Add(1 * time.Hour),
		Format: "json",
	}

	result, err := exporter.ExportToJSON(ctx, buf, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify result
	if result.MetricsExported != 2 {
		t.Errorf("Expected 2 metrics exported, got %d", result.MetricsExported)
	}

	// Parse JSON output
	var exportData ImportData
	if err := json.Unmarshal(buf.Bytes(), &exportData); err != nil {
		t.Fatalf("Failed to parse exported JSON: %v", err)
	}

	// Verify metadata
	if exportData.Metadata.Format != "json" {
		t.Errorf("Expected format 'json', got %s", exportData.Metadata.Format)
	}
	if exportData.Metadata.MetricCount != 2 {
		t.Errorf("Expected metric count 2, got %d", exportData.Metadata.MetricCount)
	}

	// Verify metrics
	if len(exportData.Metrics) != 2 {
		t.Errorf("Expected 2 metrics in output, got %d", len(exportData.Metrics))
	}
}

func TestExportToCSV(t *testing.T) {
	// Create in-memory storage
	store := memory.New()
	defer store.Close()

	// Write test metrics with various labels
	ctx := context.Background()
	testMetrics := []metrics.Metric{
		{
			Name:      "http_requests",
			Type:      metrics.CounterType,
			Value:     100,
			Labels:    map[string]string{"service": "api", "endpoint": "/users"},
			Timestamp: time.Date(2025, 11, 19, 12, 0, 0, 0, time.UTC),
		},
		{
			Name:      "http_requests",
			Type:      metrics.CounterType,
			Value:     200,
			Labels:    map[string]string{"service": "api", "endpoint": "/posts", "status": "200"},
			Timestamp: time.Date(2025, 11, 19, 12, 1, 0, 0, time.UTC),
		},
	}

	if err := store.Write(ctx, testMetrics); err != nil {
		t.Fatalf("Failed to write test metrics: %v", err)
	}

	// Export to CSV
	exporter := NewExporter(store)
	buf := &bytes.Buffer{}
	opts := ExportOptions{
		Start:  time.Date(2025, 11, 19, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2025, 11, 19, 23, 59, 59, 0, time.UTC),
		Format: "csv",
	}

	result, err := exporter.ExportToCSV(ctx, buf, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify result
	if result.MetricsExported != 2 {
		t.Errorf("Expected 2 metrics exported, got %d", result.MetricsExported)
	}

	// Parse CSV output
	reader := csv.NewReader(buf)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to parse CSV: %v", err)
	}

	// Verify header + 2 data rows
	if len(records) != 3 {
		t.Errorf("Expected 3 CSV records (header + 2 rows), got %d", len(records))
	}

	// Verify header contains expected columns
	header := records[0]
	expectedCols := []string{"timestamp", "name", "type", "value"}
	for i, col := range expectedCols {
		if header[i] != col {
			t.Errorf("Expected column %d to be %s, got %s", i, col, header[i])
		}
	}

	// Verify label columns are present (order may vary)
	headerStr := strings.Join(header, ",")
	if !strings.Contains(headerStr, "endpoint") {
		t.Error("CSV header missing 'endpoint' column")
	}
	if !strings.Contains(headerStr, "service") {
		t.Error("CSV header missing 'service' column")
	}
}

func TestImportFromJSON(t *testing.T) {
	// Create in-memory storage
	store := memory.New()
	defer store.Close()

	// Create test import data
	importData := ImportData{
		Metrics: []metrics.Metric{
			{
				Name:      "imported_counter",
				Type:      metrics.CounterType,
				Value:     99,
				Labels:    map[string]string{"source": "backup"},
				Timestamp: time.Now(),
			},
			{
				Name:      "imported_gauge",
				Type:      metrics.GaugeType,
				Value:     3.14,
				Labels:    map[string]string{"source": "backup"},
				Timestamp: time.Now(),
			},
		},
	}
	importData.Metadata.MetricCount = len(importData.Metrics)
	importData.Metadata.Format = "json"
	importData.Metadata.Version = "1.0"

	// Marshal to JSON
	jsonData, err := json.Marshal(importData)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	// Import metrics
	importer := NewImporter(store)
	ctx := context.Background()
	result, err := importer.ImportFromJSON(ctx, bytes.NewReader(jsonData))
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	// Verify result
	if result.MetricsImported != 2 {
		t.Errorf("Expected 2 metrics imported, got %d", result.MetricsImported)
	}
	if result.BatchesWritten != 1 {
		t.Errorf("Expected 1 batch written, got %d", result.BatchesWritten)
	}
	if len(result.Errors) > 0 {
		t.Errorf("Expected no validation errors, got %d: %v", len(result.Errors), result.Errors)
	}

	// Verify metrics are in storage
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.TotalMetrics != 2 {
		t.Errorf("Expected 2 metrics in storage, got %d", stats.TotalMetrics)
	}
}

func TestImportValidation(t *testing.T) {
	// Create in-memory storage
	store := memory.New()
	defer store.Close()

	// Create test data with some invalid metrics
	importData := ImportData{
		Metrics: []metrics.Metric{
			{
				Name:      "valid_metric",
				Type:      metrics.CounterType,
				Value:     10,
				Timestamp: time.Now(),
			},
			{
				Name:      "", // Invalid: empty name
				Type:      metrics.CounterType,
				Value:     20,
				Timestamp: time.Now(),
			},
			{
				Name:      "invalid_type",
				Type:      "not_a_real_type", // Invalid type
				Value:     30,
				Timestamp: time.Now(),
			},
			{
				Name:      "future_metric",
				Type:      metrics.GaugeType,
				Value:     40,
				Timestamp: time.Now().Add(48 * time.Hour), // Too far in future
			},
		},
	}

	jsonData, _ := json.Marshal(importData)

	// Import metrics
	importer := NewImporter(store)
	ctx := context.Background()
	result, err := importer.ImportFromJSON(ctx, bytes.NewReader(jsonData))
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	// Should import only 1 valid metric, skip 3 invalid ones
	if result.MetricsImported != 1 {
		t.Errorf("Expected 1 valid metric imported, got %d", result.MetricsImported)
	}
	if len(result.Errors) != 3 {
		t.Errorf("Expected 3 validation errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestExportEmptyStorage(t *testing.T) {
	// Create empty storage
	store := memory.New()
	defer store.Close()

	// Export from empty storage
	exporter := NewExporter(store)
	buf := &bytes.Buffer{}
	opts := ExportOptions{
		Start:  time.Now().Add(-1 * time.Hour),
		End:    time.Now(),
		Format: "json",
	}

	result, err := exporter.ExportToJSON(context.Background(), buf, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if result.MetricsExported != 0 {
		t.Errorf("Expected 0 metrics exported from empty storage, got %d", result.MetricsExported)
	}
}
