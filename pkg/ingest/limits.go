package ingest

import (
	"fmt"

	"tinyobs/pkg/sdk/metrics"
)

// Cardinality and validation limits
const (
	// Per-metric limits
	MaxLabelsPerMetric  = 20   // Maximum labels per metric
	MaxLabelKeyLength   = 256  // Maximum label key length
	MaxLabelValueLength = 1024 // Maximum label value length
	MaxMetricNameLength = 256  // Maximum metric name length

	// Global limits
	MaxUniqueSeries      = 100000 // Maximum unique time series (configurable in future)
	MaxSeriesPerMetric   = 10000  // Maximum series per metric name
	MaxMetricsPerRequest = 1000   // Maximum metrics in single ingest request
)

var (
	// ErrTooManyLabels is returned when a metric has too many labels
	ErrTooManyLabels = fmt.Errorf("too many labels (max %d)", MaxLabelsPerMetric)

	// ErrLabelKeyTooLong is returned when a label key is too long
	ErrLabelKeyTooLong = fmt.Errorf("label key too long (max %d chars)", MaxLabelKeyLength)

	// ErrLabelValueTooLong is returned when a label value is too long
	ErrLabelValueTooLong = fmt.Errorf("label value too long (max %d chars)", MaxLabelValueLength)

	// ErrMetricNameTooLong is returned when a metric name is too long
	ErrMetricNameTooLong = fmt.Errorf("metric name too long (max %d chars)", MaxMetricNameLength)

	// ErrMetricNameEmpty is returned when a metric name is empty
	ErrMetricNameEmpty = fmt.Errorf("metric name cannot be empty")

	// ErrCardinalityLimit is returned when the total series limit is exceeded
	ErrCardinalityLimit = fmt.Errorf("cardinality limit exceeded (max %d unique series)", MaxUniqueSeries)

	// ErrMetricCardinalityLimit is returned when a single metric's series limit is exceeded
	ErrMetricCardinalityLimit = fmt.Errorf("metric cardinality limit exceeded (max %d series per metric)", MaxSeriesPerMetric)

	// ErrTooManyMetrics is returned when an ingest request contains too many metrics
	ErrTooManyMetrics = fmt.Errorf("too many metrics in request (max %d)", MaxMetricsPerRequest)
)

// ValidateMetric validates a metric against cardinality limits
func ValidateMetric(m metrics.Metric) error {
	// Validate metric name
	if m.Name == "" {
		return ErrMetricNameEmpty
	}
	if len(m.Name) > MaxMetricNameLength {
		return fmt.Errorf("%w: %q has %d chars", ErrMetricNameTooLong, m.Name, len(m.Name))
	}

	// Validate number of labels
	if len(m.Labels) > MaxLabelsPerMetric {
		return fmt.Errorf("%w: metric %q has %d labels", ErrTooManyLabels, m.Name, len(m.Labels))
	}

	// Validate each label
	for k, v := range m.Labels {
		if len(k) > MaxLabelKeyLength {
			return fmt.Errorf("%w: key %q in metric %q", ErrLabelKeyTooLong, k, m.Name)
		}
		if len(v) > MaxLabelValueLength {
			return fmt.Errorf("%w: value for key %q in metric %q", ErrLabelValueTooLong, k, m.Name)
		}
	}

	return nil
}
