# metrics

Core metric types: Counter, Gauge, Histogram.

## Counter - Only Goes Up

```go
counter := client.Counter("requests_total")
counter.Inc("endpoint", "/users")
counter.Add(5, "endpoint", "/batch")
```

Use for: requests, errors, bytes transferred

Rejects negative values (counters can't decrease).

## Gauge - Goes Up and Down

```go
gauge := client.Gauge("queue_size")
gauge.Set(42)
gauge.Inc() / gauge.Dec()
gauge.Add(10) / gauge.Sub(5)
```

Use for: queue size, memory usage, active connections

## Histogram - Distributions

```go
histogram := client.Histogram("request_duration_seconds")
histogram.Observe(0.034, "endpoint", "/users")

// Get stats
count, sum, min, max, avg := histogram.GetStats()
p95 := histogram.GetPercentile(0.95)
p99 := histogram.GetPercentile(0.99)
```

Use for: latencies, response sizes, durations

**Warning:** Stores all observations. Can grow unbounded.

## Labels

```go
counter.Inc("endpoint", "/users", "method", "GET", "status", "200")
```

Passed as alternating key-value pairs. Odd number of labels? Last one dropped.

### Best Practices

```go
// ✅ GOOD - Low cardinality
counter.Inc("status", "200")           // ~50 values
counter.Inc("endpoint", "/users")      // ~100s of values

// ❌ BAD - High cardinality
counter.Inc("user_id", "12345")        // Millions of values!
counter.Inc("timestamp", "2025-11...")  // Infinite!
```

Rule: If label has >1000 unique values, it's probably wrong.

## Thread Safety

All operations use `sync.RWMutex`. Safe for concurrent use.

## Performance

- Counter.Inc(): ~100ns
- Gauge.Set(): ~100ns
- Histogram.Observe(): ~200ns

## Test Coverage: 0.0%

Needs tests!
