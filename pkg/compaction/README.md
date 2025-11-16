# compaction

Multi-resolution storage through downsampling and compaction.

## Purpose

Raw metrics at 15-second intervals consume a lot of storage. For a system collecting 10k metrics/sec:

- **Raw (15s)**: 2.5 TB/year
- **With 5m aggregates**: 200 GB/year (~12x reduction)
- **With 1h aggregates**: 20 GB/year (~125x reduction total)

Compaction makes long-term retention affordable.

## How it works

```
┌──────────────────────────────────────────────┐
│ Raw samples (6 hours)                         │
│ - Every 15 seconds                           │
│ - Full precision                             │
│ - Recent debugging needs                     │
└──────────────────────────────────────────────┘
              ↓ Compact every hour
┌──────────────────────────────────────────────┐
│ 5-minute aggregates (7 days)                 │
│ - Sum, Count, Min, Max                       │
│ - Can still calculate average, rate          │
│ - Weekly trend analysis                      │
└──────────────────────────────────────────────┘
              ↓ Compact daily
┌──────────────────────────────────────────────┐
│ 1-hour aggregates (30+ days)                 │
│ - Long-term trends                           │
│ - Capacity planning                          │
│ - Year-over-year comparisons                 │
└──────────────────────────────────────────────┘
```

## Usage

```go
compactor := compaction.New(storage)

// Run compaction (typically in a background job)
err := compactor.CompactAndCleanup(ctx)
```

## Why store Sum + Count instead of Average?

Averages can't be re-averaged. If you have hourly averages and want daily averages, you can't just average the averages.

But with sum + count:
```
DailyAverage = Sum(hourly sums) / Sum(hourly counts)
```

This is why we store raw aggregation components, not computed metrics.

## Performance

Compaction is I/O bound. On SSD:
- 5m compaction: ~1M metrics/sec
- 1h compaction: ~5M metrics/sec

Runs in background without blocking ingestion.
