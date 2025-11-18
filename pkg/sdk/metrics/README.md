# metrics

Core metric types for TinyObs: Counter, Gauge, and Histogram.

## The Three Metric Types

### Counter - Only Goes Up

Use for things that accumulate:
- Total HTTP requests
- Errors
- Bytes transferred
- Items processed

```go
counter := client.Counter("requests_total")
counter.Inc("endpoint", "/users")        // Increment by 1
counter.Add(5, "endpoint", "/batch")     // Add 5
```

**Characteristics:**
- ✅ Can only increase (or reset to zero)
- ❌ Cannot decrease
- ✅ Survives restarts (if you track totals)
- 📊 Best visualized as rate (requests/sec)

### Gauge - Goes Up and Down

Use for current values:
- Queue size
- Temperature
- Memory usage
- Active connections

```go
gauge := client.Gauge("queue_size")
gauge.Set(42)           // Set to absolute value
gauge.Inc()             // Increment by 1
gauge.Dec()             // Decrement by 1
gauge.Add(10)           // Add 10
gauge.Sub(5)            // Subtract 5
```

**Characteristics:**
- ✅ Can increase or decrease
- ✅ Represents current state
- ❌ Value lost on restart
- 📊 Best visualized as current value

### Histogram - Tracks Distributions

Use for measuring distributions:
- Request latencies
- Response sizes
- Database query times
- Processing durations

```go
histogram := client.Histogram("request_duration_seconds")

start := time.Now()
// ... do work ...
duration := time.Since(start).Seconds()

histogram.Observe(duration, "endpoint", "/users")
```

**Characteristics:**
- ✅ Stores all observations
- ✅ Calculate percentiles (p50, p95, p99)
- ✅ Calculate avg, min, max
- ⚠️ Memory grows with observations
- 📊 Best visualized as percentile lines

## Counter

### API

```go
type Counter interface {
    Inc(labels ...string)              // Increment by 1
    Add(value float64, labels ...string) // Add custom amount
}
```

### Usage

```go
// Create counter
requests := client.Counter("http_requests_total")

// Count requests
requests.Inc("method", "GET", "endpoint", "/users")
requests.Inc("method", "POST", "endpoint", "/orders")

// Add multiple
requests.Add(10, "method", "GET", "endpoint", "/batch")
```

### Internal Storage

Counters maintain cumulative values per label combination:

```go
values := map[string]float64{
    "method=GET,endpoint=/users":  1547,
    "method=POST,endpoint=/orders": 892,
}
```

### Thread Safety

Thread-safe with `sync.RWMutex`:
```go
c.mu.Lock()
c.values[key] += value
c.mu.Unlock()
```

Multiple goroutines can safely increment the same counter.

### Validation

Counters reject negative values:
```go
func (c *Counter) Add(value float64, labels ...string) {
    if value < 0 {
        return  // Silently ignore
    }
    // ...
}
```

**Why?** Counters represent cumulative totals. Negative increments don't make sense.

## Gauge

### API

```go
type Gauge interface {
    Set(value float64, labels ...string)     // Set to absolute value
    Inc(labels ...string)                    // Increment by 1
    Dec(labels ...string)                    // Decrement by 1
    Add(value float64, labels ...string)     // Add amount
    Sub(value float64, labels ...string)     // Subtract amount
}
```

### Usage

```go
// Create gauge
queueSize := client.Gauge("job_queue_size")
temperature := client.Gauge("room_temperature")

// Set absolute values
queueSize.Set(42)
temperature.Set(23.5, "room", "server-room")

// Track changes
func enqueueJob() {
    queueSize.Inc()  // Add job
}

func dequeueJob() {
    queueSize.Dec()  // Remove job
}
```

### Use Cases

**Active connections:**
```go
connections := client.Gauge("active_connections")

func handleConnection(conn net.Conn) {
    connections.Inc()
    defer connections.Dec()

    // Handle connection...
}
```

**Resource usage:**
```go
memory := client.Gauge("memory_usage_bytes")

func monitorMemory() {
    ticker := time.NewTicker(10 * time.Second)
    for range ticker.C {
        var m runtime.MemStats
        runtime.ReadMemStats(&m)
        memory.Set(float64(m.HeapAlloc))
    }
}
```

### Internal Storage

Gauges store current values:

```go
values := map[string]float64{
    "": 42,                      // No labels
    "room=server-room": 23.5,    // With labels
}
```

## Histogram

### API

```go
type Histogram interface {
    Observe(value float64, labels ...string)  // Record observation
    GetStats(labels ...string) (count int, sum, min, max, avg float64)
    GetPercentile(percentile float64, labels ...string) float64
}
```

### Usage

```go
// Create histogram
latency := client.Histogram("request_duration_seconds")

// Measure latency
start := time.Now()
processRequest()
duration := time.Since(start).Seconds()

latency.Observe(duration, "endpoint", "/users")

// Get statistics
count, sum, min, max, avg := latency.GetStats("endpoint", "/users")
p95 := latency.GetPercentile(0.95, "endpoint", "/users")
p99 := latency.GetPercentile(0.99, "endpoint", "/users")
```

### Percentiles

Histograms can calculate percentiles:

```go
p50 := histogram.GetPercentile(0.50)  // Median
p95 := histogram.GetPercentile(0.95)  // 95th percentile
p99 := histogram.GetPercentile(0.99)  // 99th percentile
```

**What's a percentile?**
- **p50 (median)**: 50% of requests are faster than this
- **p95**: 95% of requests are faster than this
- **p99**: 99% of requests are faster than this

**Why p95/p99?** They show "worst case" performance better than averages.

### Internal Storage

Histograms store all observations:

```go
values := map[string][]float64{
    "endpoint=/users": {0.023, 0.034, 0.012, 0.045, ...},
    "endpoint=/orders": {0.142, 0.156, 0.089, ...},
}
```

**Memory Warning:** Histograms grow unbounded. For high-traffic apps, consider using buckets or server-side aggregation.

### Statistics

```go
count, sum, min, max, avg := histogram.GetStats()

fmt.Printf("Count: %d\n", count)         // Number of observations
fmt.Printf("Sum: %.3f\n", sum)           // Total of all values
fmt.Printf("Min: %.3f\n", min)           // Smallest value
fmt.Printf("Max: %.3f\n", max)           // Largest value
fmt.Printf("Avg: %.3f\n", avg)           // Average (sum/count)
```

## Labels

All three metric types support labels:

```go
counter.Inc("endpoint", "/users", "method", "GET", "status", "200")
gauge.Set(42, "queue", "high-priority")
histogram.Observe(0.034, "endpoint", "/users", "status", "200")
```

### Label Format

Labels are passed as **alternating key-value pairs**:

```go
metric.Inc("key1", "value1", "key2", "value2")
```

Internally converted to:
```go
map[string]string{
    "key1": "value1",
    "key2": "value2",
}
```

### Odd Number of Labels

If you pass an odd number of labels, the last one is ignored:

```go
counter.Inc("endpoint", "/users", "orphan")
// Result: {endpoint: "/users"}
// "orphan" is silently dropped
```

**Why not error?** To avoid breaking apps with metric bugs. Better to lose one label than crash.

### Label Best Practices

```go
// ✅ GOOD - Low cardinality
counter.Inc("method", "GET")           // ~10 values (GET, POST, ...)
counter.Inc("status", "200")           // ~50 values (100-599)
counter.Inc("endpoint", "/users")      // ~100s of values

// ❌ BAD - High cardinality
counter.Inc("user_id", "12345")        // Millions of values!
counter.Inc("timestamp", "2025-11...")  // Infinite values!
counter.Inc("request_id", uuid.New())   // Infinite values!
```

**Rule of thumb:** If a label has >1000 unique values, it's probably wrong.

## Thread Safety

All metric types are thread-safe:

```go
// Safe from multiple goroutines
for i := 0; i < 100; i++ {
    go func() {
        counter.Inc()  // No race condition
    }()
}
```

**Implementation:** Uses `sync.RWMutex` for synchronization.

**Performance:** Lock contention can be a bottleneck with >1000 goroutines incrementing the same metric. For extreme cases, consider sharding.

## Performance

Benchmarks on modern hardware:

| Operation | Time | Allocations |
|-----------|------|-------------|
| Counter.Inc() | ~100ns | 0 allocs (cached) |
| Gauge.Set() | ~100ns | 0 allocs |
| Histogram.Observe() | ~200ns | 1 alloc (append) |
| Counter (new labels) | ~500ns | 1 alloc (map insert) |

**Takeaway:** Metrics are cheap. Don't worry about overhead unless you're incrementing millions of times per second.

## Common Patterns

### Track Request Lifecycle

```go
requests := client.Counter("requests_total")
inFlight := client.Gauge("requests_in_flight")
duration := client.Histogram("request_duration_seconds")

func handleRequest(w http.ResponseWriter, r *http.Request) {
    start := time.Now()
    inFlight.Inc()
    defer inFlight.Dec()

    // Handle request...

    requests.Inc("endpoint", r.URL.Path)
    duration.Observe(time.Since(start).Seconds(), "endpoint", r.URL.Path)
}
```

### Rate Limiting

```go
rateLimitCounter := client.Counter("rate_limit_hits_total")

func checkRateLimit(user string) bool {
    // Check rate limit...
    if limited {
        rateLimitCounter.Inc("user", user)
        return false
    }
    return true
}
```

### Background Job Queue

```go
jobsQueued := client.Gauge("jobs_queued")
jobsProcessed := client.Counter("jobs_processed_total")
jobDuration := client.Histogram("job_duration_seconds")

func enqueueJob(job Job) {
    jobsQueued.Inc()
    // Queue job...
}

func processJob(job Job) {
    start := time.Now()
    defer jobsQueued.Dec()

    // Process job...

    jobsProcessed.Inc("type", job.Type)
    jobDuration.Observe(time.Since(start).Seconds(), "type", job.Type)
}
```

## Testing

### Example Test

```go
func TestCounter(t *testing.T) {
    client := &mockClient{}
    counter := NewCounter("test_counter", client)

    counter.Inc("label", "value")

    if len(client.metrics) != 1 {
        t.Errorf("Expected 1 metric, got %d", len(client.metrics))
    }

    if client.metrics[0].Value != 1 {
        t.Errorf("Expected value 1, got %f", client.metrics[0].Value)
    }
}

type mockClient struct {
    metrics []Metric
}

func (m *mockClient) SendMetric(metric Metric) {
    m.metrics = append(m.metrics, metric)
}
```

## Comparison to Prometheus

| Feature | TinyObs | Prometheus |
|---------|---------|------------|
| Counter | ✅ Same concept | ✅ |
| Gauge | ✅ Same concept | ✅ |
| Histogram | ⚠️ Stores all values | 📊 Uses buckets |
| Summary | ❌ Not implemented | ✅ |
| Native histograms | ❌ | ✅ (experimental) |

**Key difference:** TinyObs histograms store every observation, while Prometheus uses pre-defined buckets. TinyObs is more flexible but uses more memory.

## Test Coverage

Coverage: **0.0%** (as of v2.2)

**This package needs tests!** Contributions welcome.

Suggested test cases:
- Counter validation (negative values)
- Gauge Inc/Dec operations
- Histogram percentile calculation
- Label parsing (odd/even counts)
- Thread safety (concurrent increments)

## See Also

- `pkg/sdk/` - Main SDK client that creates metrics
- `pkg/sdk/httpx/` - Auto-creates HTTP metrics
- `pkg/ingest/` - Server that stores metric data
