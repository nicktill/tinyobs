/*
Package compaction implements multi-resolution downsampling for TinyObs metrics.

# What is Downsampling?

Raw metrics consume massive storage. If you collect CPU usage every second for a year:
  - 1 sample/sec × 86,400 sec/day × 365 days = 31.5 million data points

Downsampling aggregates old data into larger time buckets, reducing storage by 240x:

	Raw (1s intervals)     → 100% of original size
	5-minute aggregates    → 4% of original size (20x compression)
	1-hour aggregates      → 0.4% of original size (240x compression)

# How TinyObs Compaction Works

TinyObs uses a three-tier retention strategy:

	┌─────────────────────────────────────────────────────────────┐
	│ Raw Data (0-14 days)                                        │
	│ • Full resolution (every sample)                            │
	│ • Used for recent, detailed queries                         │
	│ • Example: "Show me CPU spikes in the last hour"           │
	└─────────────────────────────────────────────────────────────┘
	                      ↓ Compact after 6 hours
	┌─────────────────────────────────────────────────────────────┐
	│ 5-minute Aggregates (14-90 days)                            │
	│ • One aggregate per 5-minute window                         │
	│ • Used for weekly/monthly trends                            │
	│ • Example: "Show me average CPU over the past 30 days"     │
	└─────────────────────────────────────────────────────────────┘
	                      ↓ Compact after 2 days
	┌─────────────────────────────────────────────────────────────┐
	│ 1-hour Aggregates (90 days - 1 year)                        │
	│ • One aggregate per hour                                    │
	│ • Used for long-term historical trends                      │
	│ • Example: "Show me traffic patterns over the past year"   │
	└─────────────────────────────────────────────────────────────┘

# Aggregate Structure

Each aggregate stores five values to support different query patterns:

	type Aggregate struct {
	    Sum   float64  // Total of all values (for counters)
	    Count int      // Number of data points
	    Min   float64  // Minimum value (for anomaly detection)
	    Max   float64  // Maximum value (for peak tracking)
	    Avg   float64  // Average = Sum / Count
	}

Why all five? Different metrics need different aggregates:
  - Request count: Use Sum (total requests in window)
  - CPU usage: Use Avg (average CPU over window)
  - Error rate: Use Max (peak errors in window)
  - Response time: Use Min/Max (best/worst case latency)

# Usage Example

	import (
	    "context"
	    "time"
	    "github.com/nicktill/tinyobs/pkg/compaction"
	    "github.com/nicktill/tinyobs/pkg/storage/badger"
	)

	// Create storage and compactor
	store, _ := badger.New(badger.Config{Path: "./data"})
	compactor := compaction.New(store)

	// Compact raw data into 5-minute aggregates
	// This runs hourly in production
	start := time.Now().Add(-24 * time.Hour)
	end := time.Now()
	err := compactor.Compact5m(context.Background(), start, end)

	// Compact 5-minute aggregates into 1-hour aggregates
	// This also runs hourly for older data
	err = compactor.Compact1h(context.Background(), start, end)

# How Compact5m Works

Input (raw metrics, 1-second intervals):
	2024-11-19 10:00:00  cpu=45.2
	2024-11-19 10:00:01  cpu=46.1
	2024-11-19 10:00:02  cpu=44.8
	2024-11-19 10:00:03  cpu=47.5
	... (300 samples in 5 minutes)
	2024-11-19 10:04:59  cpu=45.9

Output (5-minute aggregate):
	2024-11-19 10:00:00  cpu_sum=13,575  cpu_count=300  cpu_min=42.0  cpu_max=48.1  cpu_avg=45.25
	__resolution__=5m

Result: 300 data points → 1 aggregate (99.7% reduction)

# How Compact1h Works

Input (5-minute aggregates):
	2024-11-19 10:00:00  cpu_sum=13,575  cpu_count=300  cpu_avg=45.25
	2024-11-19 10:05:00  cpu_sum=13,650  cpu_count=300  cpu_avg=45.50
	2024-11-19 10:10:00  cpu_sum=13,425  cpu_count=300  cpu_avg=44.75
	... (12 aggregates in 1 hour)

Output (1-hour aggregate):
	2024-11-19 10:00:00  cpu_sum=162,900  cpu_count=3,600  cpu_min=42.0  cpu_max=52.3  cpu_avg=45.25
	__resolution__=1h

Result: 12 aggregates → 1 aggregate (92% further reduction)

Combined: 3,600 raw samples → 1 hourly aggregate (99.97% reduction!)

# Compaction Timing

Compaction doesn't happen immediately. TinyObs waits before compacting to ensure:
1. All data for a time window has been received (no late arrivals)
2. Recent data stays at high resolution for detailed queries

Timing constants:
  - Raw → 5m: Wait 6 hours after data is written
  - 5m → 1h: Wait 2 days after 5m aggregate is created
  - Raw retention: Delete after 14 days
  - 5m retention: Delete after 90 days

Example timeline for a metric written at 10:00 AM on Nov 19:
  - Nov 19 10:00 AM: Written as raw data
  - Nov 19 4:00 PM: Compacted into 5m aggregate (6 hours later)
  - Nov 21 4:00 PM: Compacted into 1h aggregate (2 days later)
  - Dec 3 10:00 AM: Raw data deleted (14 days)
  - Feb 17 10:00 AM: 5m aggregate deleted (90 days)
  - Nov 19 next year: 1h aggregate deleted (1 year)

# Performance Impact

Compaction is expensive (CPU + I/O), so TinyObs runs it hourly, not continuously.

Typical compaction metrics (10,000 series):
  - Duration: ~30 seconds
  - CPU usage: 50-80% (single core)
  - Storage I/O: 100-500 MB read + 50-200 MB write
  - Memory: <100 MB

# Best Practices

1. Run compaction during low-traffic periods if possible
2. Monitor compaction errors (e.g., via /v1/health)
3. Don't compact very recent data (wait for the time window to complete)
4. Use context.WithTimeout() to prevent hung compactions
5. If compaction fails, retry (it's idempotent)

# Error Handling

Compaction can fail for several reasons:
  - Disk full (no space for aggregates)
  - Storage corruption (BadgerDB integrity error)
  - Query timeout (too much data to process)

Always check errors and log them:

	err := compactor.Compact5m(ctx, start, end)
	if err != nil {
	    log.Errorf("5m compaction failed: %v", err)
	    // Don't panic - compaction will retry next hour
	}

Compaction is idempotent: running it twice on the same time range is safe.

# See Also

  - pkg/storage for the Storage interface
  - docs/ARCHITECTURE.md for high-level design
  - cmd/server/main.go for production compaction loop
*/
package compaction
