package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

const (
	// MaxImportBatchSize is the maximum number of metrics to write at once
	MaxImportBatchSize = 5000
)

// Importer handles importing metrics from backup files
type Importer struct {
	storage storage.Storage
}

// NewImporter creates a new importer
func NewImporter(store storage.Storage) *Importer {
	return &Importer{storage: store}
}

// ImportResult contains stats about the import operation
type ImportResult struct {
	MetricsImported int       `json:"metrics_imported"`
	BatchesWritten  int       `json:"batches_written"`
	TimeRange       string    `json:"time_range"`
	ImportedAt      time.Time `json:"imported_at"`
	Errors          []string  `json:"errors,omitempty"`
}

// ImportData represents the structure of imported JSON data
type ImportData struct {
	Metadata struct {
		ExportedAt  time.Time `json:"exported_at"`
		StartTime   time.Time `json:"start_time"`
		EndTime     time.Time `json:"end_time"`
		MetricCount int       `json:"metric_count"`
		Format      string    `json:"format"`
		Version     string    `json:"version"`
	} `json:"metadata"`
	Metrics []metrics.Metric `json:"metrics"`
}

// ImportFromJSON imports metrics from a JSON backup file
func (im *Importer) ImportFromJSON(ctx context.Context, r io.Reader) (*ImportResult, error) {
	// Decode JSON data
	var importData ImportData
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&importData); err != nil {
		return nil, fmt.Errorf("failed to decode JSON: %w", err)
	}

	// Validate import data
	if len(importData.Metrics) == 0 {
		return &ImportResult{
			MetricsImported: 0,
			BatchesWritten:  0,
			TimeRange:       "empty",
			ImportedAt:      time.Now(),
		}, nil
	}

	// Validate metric format
	var validationErrors []string
	validMetrics := make([]metrics.Metric, 0, len(importData.Metrics))

	for i, m := range importData.Metrics {
		if err := validateImportedMetric(m); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("metric %d: %v", i, err))
			continue
		}
		validMetrics = append(validMetrics, m)
	}

	// Write metrics in batches to avoid overwhelming storage
	batchCount := 0
	for i := 0; i < len(validMetrics); i += MaxImportBatchSize {
		end := i + MaxImportBatchSize
		if end > len(validMetrics) {
			end = len(validMetrics)
		}

		batch := validMetrics[i:end]
		if err := im.storage.Write(ctx, batch); err != nil {
			return nil, fmt.Errorf("failed to write batch %d: %w", batchCount, err)
		}
		batchCount++
	}

	// Calculate time range
	var minTime, maxTime time.Time
	if len(validMetrics) > 0 {
		minTime = validMetrics[0].Timestamp
		maxTime = validMetrics[0].Timestamp
		for _, m := range validMetrics {
			if m.Timestamp.Before(minTime) {
				minTime = m.Timestamp
			}
			if m.Timestamp.After(maxTime) {
				maxTime = m.Timestamp
			}
		}
	}

	result := &ImportResult{
		MetricsImported: len(validMetrics),
		BatchesWritten:  batchCount,
		TimeRange:       fmt.Sprintf("%s to %s", minTime.Format(time.RFC3339), maxTime.Format(time.RFC3339)),
		ImportedAt:      time.Now(),
		Errors:          validationErrors,
	}

	return result, nil
}

// validateImportedMetric validates a metric before import
func validateImportedMetric(m metrics.Metric) error {
	if m.Name == "" {
		return fmt.Errorf("metric name cannot be empty")
	}

	if m.Type != metrics.CounterType && m.Type != metrics.GaugeType && m.Type != metrics.HistogramType {
		return fmt.Errorf("invalid metric type: %q", m.Type)
	}

	if m.Timestamp.IsZero() {
		return fmt.Errorf("metric timestamp cannot be zero")
	}

	// Check for reasonable timestamp (not too far in past/future)
	now := time.Now()
	if m.Timestamp.Before(now.Add(-10 * 365 * 24 * time.Hour)) {
		return fmt.Errorf("timestamp too far in past: %s", m.Timestamp)
	}
	if m.Timestamp.After(now.Add(24 * time.Hour)) {
		return fmt.Errorf("timestamp too far in future: %s", m.Timestamp)
	}

	return nil
}
