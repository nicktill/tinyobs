package export

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// Exporter handles exporting metrics to various formats
type Exporter struct {
	storage storage.Storage
}

// NewExporter creates a new exporter
func NewExporter(store storage.Storage) *Exporter {
	return &Exporter{storage: store}
}

// ExportOptions configures the export operation
type ExportOptions struct {
	// Time range to export
	Start time.Time
	End   time.Time

	// Filter by metric names (nil = all metrics)
	MetricNames []string

	// Filter by labels (nil = no label filtering)
	Labels map[string]string

	// Format: "json" or "csv"
	Format string
}

// ExportResult contains stats about the export
type ExportResult struct {
	MetricsExported int       `json:"metrics_exported"`
	TimeRange       string    `json:"time_range"`
	Format          string    `json:"format"`
	ExportedAt      time.Time `json:"exported_at"`
}

// ExportToJSON exports metrics as JSON to the given writer
func (e *Exporter) ExportToJSON(ctx context.Context, w io.Writer, opts ExportOptions) (*ExportResult, error) {
	// Query metrics from storage
	queryReq := storage.QueryRequest{
		Start:       opts.Start,
		End:         opts.End,
		MetricNames: opts.MetricNames,
		Labels:      opts.Labels,
		Limit:       0, // No limit - export everything
	}

	metricsData, err := e.storage.Query(ctx, queryReq)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}

	// Create export wrapper with metadata
	exportData := struct {
		Metadata struct {
			ExportedAt  time.Time `json:"exported_at"`
			StartTime   time.Time `json:"start_time"`
			EndTime     time.Time `json:"end_time"`
			MetricCount int       `json:"metric_count"`
			Format      string    `json:"format"`
			Version     string    `json:"version"`
		} `json:"metadata"`
		Metrics []metrics.Metric `json:"metrics"`
	}{
		Metrics: metricsData,
	}

	exportData.Metadata.ExportedAt = time.Now()
	exportData.Metadata.StartTime = opts.Start
	exportData.Metadata.EndTime = opts.End
	exportData.Metadata.MetricCount = len(metricsData)
	exportData.Metadata.Format = "json"
	exportData.Metadata.Version = "1.0"

	// Encode as pretty JSON
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(exportData); err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	result := &ExportResult{
		MetricsExported: len(metricsData),
		TimeRange:       fmt.Sprintf("%s to %s", opts.Start.Format(time.RFC3339), opts.End.Format(time.RFC3339)),
		Format:          "json",
		ExportedAt:      exportData.Metadata.ExportedAt,
	}

	return result, nil
}

// ExportToCSV exports metrics as CSV to the given writer
func (e *Exporter) ExportToCSV(ctx context.Context, w io.Writer, opts ExportOptions) (*ExportResult, error) {
	// Query metrics from storage
	queryReq := storage.QueryRequest{
		Start:       opts.Start,
		End:         opts.End,
		MetricNames: opts.MetricNames,
		Labels:      opts.Labels,
		Limit:       0, // No limit - export everything
	}

	metricsData, err := e.storage.Query(ctx, queryReq)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}

	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Collect all unique label keys across all metrics for consistent columns
	labelKeys := collectLabelKeys(metricsData)

	// Write CSV header
	header := []string{"timestamp", "name", "type", "value"}
	header = append(header, labelKeys...)
	if err := writer.Write(header); err != nil {
		return nil, fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write data rows
	for _, m := range metricsData {
		row := []string{
			m.Timestamp.Format(time.RFC3339),
			m.Name,
			string(m.Type),
			strconv.FormatFloat(m.Value, 'f', -1, 64),
		}

		// Add label values in consistent order
		for _, key := range labelKeys {
			if val, ok := m.Labels[key]; ok {
				row = append(row, val)
			} else {
				row = append(row, "") // Empty if label not present
			}
		}

		if err := writer.Write(row); err != nil {
			return nil, fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	result := &ExportResult{
		MetricsExported: len(metricsData),
		TimeRange:       fmt.Sprintf("%s to %s", opts.Start.Format(time.RFC3339), opts.End.Format(time.RFC3339)),
		Format:          "csv",
		ExportedAt:      time.Now(),
	}

	return result, nil
}

// collectLabelKeys gathers all unique label keys from metrics and returns them sorted
func collectLabelKeys(metricsData []metrics.Metric) []string {
	keySet := make(map[string]bool)
	for _, m := range metricsData {
		for key := range m.Labels {
			keySet[key] = true
		}
	}

	keys := make([]string, 0, len(keySet))
	for key := range keySet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
