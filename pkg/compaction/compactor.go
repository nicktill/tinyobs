package compaction

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"tinyobs/pkg/sdk/metrics"
	"tinyobs/pkg/storage"
)

const (
	// Compaction timing windows
	compact5mDelay     = 6 * time.Hour  // Wait before compacting raw data
	compact5mLookback  = 12 * time.Hour // How far back to compact
	compact1hDelay     = 2 * 24 * time.Hour  // Wait before compacting 5m aggregates
	compact1hLookback  = 7 * 24 * time.Hour  // How far back to compact 5m->1h
	rawDataRetention   = 6 * time.Hour  // Keep raw data for 6 hours
	fiveMinRetention   = 7 * 24 * time.Hour  // Keep 5m aggregates for 7 days
)

// Compactor handles downsampling of metrics
type Compactor struct {
	storage storage.Storage
}

// New creates a new compactor
func New(store storage.Storage) *Compactor {
	return &Compactor{
		storage: store,
	}
}

// Compact5m aggregates raw metrics into 5-minute buckets
//
// This reduces storage by ~20x:
// - Raw samples every 15s = 240 samples/hour
// - 5m aggregates = 12 aggregates/hour
//
// We store sum, count, min, max so we can still calculate:
// - Average (sum/count)
// - Rate of change
// - Min/max bounds
func (c *Compactor) Compact5m(ctx context.Context, start, end time.Time) error {
	// Query raw metrics in the time range
	rawMetrics, err := c.storage.Query(ctx, storage.QueryRequest{
		Start: start,
		End:   end,
	})
	if err != nil {
		return fmt.Errorf("failed to query raw metrics: %w", err)
	}

	// Group metrics by series and 5-minute buckets
	buckets := make(map[string]*Aggregate)

	for _, m := range rawMetrics {
		// Skip existing aggregates - only compact raw metrics
		if m.Labels != nil && m.Labels["__resolution__"] != "" {
			continue
		}

		// Round timestamp to 5-minute bucket
		bucketTime := roundTo5Minutes(m.Timestamp)

		// Create unique key for this series + bucket
		// Make defensive copy of labels to avoid mutation bugs
		labelsCopy := make(map[string]string, len(m.Labels))
		for k, v := range m.Labels {
			labelsCopy[k] = v
		}
		key := aggregateKey(m.Name, labelsCopy, bucketTime)

		agg, exists := buckets[key]
		if !exists {
			agg = &Aggregate{
				Name:       m.Name,
				Labels:     labelsCopy,
				Timestamp:  bucketTime,
				Resolution: Resolution5m,
				Min:        m.Value,
				Max:        m.Value,
			}
			buckets[key] = agg
		}

		// Update aggregate
		agg.Sum += m.Value
		agg.Count++
		if m.Value < agg.Min {
			agg.Min = m.Value
		}
		if m.Value > agg.Max {
			agg.Max = m.Value
		}
	}

	// Convert aggregates to metrics and write
	aggregateMetrics := make([]metrics.Metric, 0, len(buckets))
	for _, agg := range buckets {
		aggregateMetrics = append(aggregateMetrics, agg.ToMetric())
	}

	if len(aggregateMetrics) > 0 {
		if err := c.storage.Write(ctx, aggregateMetrics); err != nil {
			return fmt.Errorf("failed to write 5m aggregates: %w", err)
		}
	}

	return nil
}

// Compact1h aggregates 5-minute buckets into 1-hour buckets
//
// This reduces storage by another ~12x:
// - 5m aggregates = 12 per hour
// - 1h aggregate = 1 per hour
//
// Total reduction: ~240x from raw samples
func (c *Compactor) Compact1h(ctx context.Context, start, end time.Time) error {
	// Query 5-minute aggregates
	// In production, would filter by resolution
	rawMetrics, err := c.storage.Query(ctx, storage.QueryRequest{
		Start: start,
		End:   end,
	})
	if err != nil {
		return fmt.Errorf("failed to query 5m aggregates: %w", err)
	}

	// Group by series and 1-hour buckets
	buckets := make(map[string]*Aggregate)

	for _, m := range rawMetrics {
		// Parse as aggregate (skip if not an aggregate or wrong resolution)
		sourceAgg := FromMetric(m)
		if sourceAgg == nil || sourceAgg.Resolution != Resolution5m {
			continue // Only re-aggregate 5m aggregates
		}

		bucketTime := roundTo1Hour(m.Timestamp)
		key := aggregateKey(m.Name, sourceAgg.Labels, bucketTime)

		agg, exists := buckets[key]
		if !exists {
			agg = &Aggregate{
				Name:       m.Name,
				Labels:     sourceAgg.Labels,
				Timestamp:  bucketTime,
				Resolution: Resolution1h,
				Min:        sourceAgg.Min,
				Max:        sourceAgg.Max,
			}
			buckets[key] = agg
		}

		// Re-aggregate by combining Sum and Count (preserves original data)
		agg.Sum += sourceAgg.Sum
		agg.Count += sourceAgg.Count
		if sourceAgg.Min < agg.Min {
			agg.Min = sourceAgg.Min
		}
		if sourceAgg.Max > agg.Max {
			agg.Max = sourceAgg.Max
		}
	}

	// Write 1h aggregates
	aggregateMetrics := make([]metrics.Metric, 0, len(buckets))
	for _, agg := range buckets {
		aggregateMetrics = append(aggregateMetrics, agg.ToMetric())
	}

	if len(aggregateMetrics) > 0 {
		if err := c.storage.Write(ctx, aggregateMetrics); err != nil {
			return fmt.Errorf("failed to write 1h aggregates: %w", err)
		}
	}

	return nil
}

// CompactAndCleanup performs downsampling and removes old raw data
// This is the main compaction job that should run periodically
func (c *Compactor) CompactAndCleanup(ctx context.Context) error {
	now := time.Now()

	// Step 1: Compact raw data from 6-12 hours ago into 5m aggregates
	// (Wait to ensure all data has arrived)
	compact5mStart := now.Add(-compact5mLookback)
	compact5mEnd := now.Add(-compact5mDelay)

	if err := c.Compact5m(ctx, compact5mStart, compact5mEnd); err != nil {
		return fmt.Errorf("5m compaction failed: %w", err)
	}

	// Step 2: Delete raw data older than retention period
	// TODO: Currently Delete removes ALL metrics, including aggregates
	// Need to implement resolution-aware deletion to only remove raw data
	// Skipping for now to avoid deleting newly created aggregates
	// See: https://github.com/yourusername/tinyobs/issues/XX
	/*
	if err := c.storage.Delete(ctx, now.Add(-rawDataRetention)); err != nil {
		return fmt.Errorf("failed to delete old raw data: %w", err)
	}
	*/

	// Step 3: Compact 5m aggregates from 2-7 days ago into 1h aggregates
	compact1hStart := now.Add(-compact1hLookback)
	compact1hEnd := now.Add(-compact1hDelay)

	if err := c.Compact1h(ctx, compact1hStart, compact1hEnd); err != nil {
		return fmt.Errorf("1h compaction failed: %w", err)
	}

	// Step 4: Delete 5m aggregates older than retention period
	// TODO: Same issue as Step 2 - need resolution-aware deletion
	// Skipping for now to avoid deleting newly created 1h aggregates
	/*
	if err := c.storage.Delete(ctx, now.Add(-fiveMinRetention)); err != nil {
		return fmt.Errorf("failed to delete old 5m aggregates: %w", err)
	}
	*/

	return nil
}

// roundTo5Minutes rounds a timestamp down to the nearest 5-minute bucket
func roundTo5Minutes(t time.Time) time.Time {
	minutes := t.Minute()
	roundedMinutes := (minutes / 5) * 5

	return time.Date(
		t.Year(), t.Month(), t.Day(),
		t.Hour(), roundedMinutes, 0, 0,
		t.Location(),
	)
}

// roundTo1Hour rounds a timestamp down to the nearest hour
func roundTo1Hour(t time.Time) time.Time {
	return time.Date(
		t.Year(), t.Month(), t.Day(),
		t.Hour(), 0, 0, 0,
		t.Location(),
	)
}

// aggregateKey creates a unique key for an aggregate
func aggregateKey(name string, labels map[string]string, timestamp time.Time) string {
	key := name + "@" + timestamp.Format(time.RFC3339)

	// Add sorted labels for deterministic key
	if len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			key += "," + k + "=" + labels[k]
		}
	}

	return key
}

// CalculatePercentile computes percentile from raw values
// Used when precise percentiles are needed (vs approximation from min/max)
func CalculatePercentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	index := p * float64(len(sorted)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sorted[lower]
	}

	// Linear interpolation
	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
