# runtime

Automatic Go runtime metrics collection. Tracks memory, goroutines, GC stats, and more.

## What It Tracks

The runtime collector automatically monitors:

| Metric | Type | Description |
|--------|------|-------------|
| `go_memstats_heap_alloc_bytes` | Gauge | Current heap memory in use |
| `go_memstats_heap_sys_bytes` | Gauge | Total heap memory from OS |
| `go_memstats_heap_objects` | Gauge | Number of allocated objects |
| `go_goroutines` | Gauge | Current number of goroutines |
| `go_gc_cycles_total` | Counter | Total GC cycles |
| `go_memstats_gc_duration_seconds` | Counter | Cumulative GC pause time |

All metrics include a `service` label matching your SDK config.

## How It Works

When you create a TinyObs client, runtime metrics are automatically collected every 10 seconds:

```go
client, _ := sdk.New(sdk.ClientConfig{Service: "my-app"})
client.Start(context.Background())
// Runtime metrics start collecting automatically
```

**Under the hood:**
```go
func (c *Client) collectRuntimeMetrics() {
    ticker := time.NewTicker(10 * time.Second)
    for range ticker.C {
        // Collect Go runtime stats
        var m runtime.MemStats
        runtime.ReadMemStats(&m)

        // Send metrics to TinyObs
        // ...
    }
}
```

## No Configuration Needed

Runtime metrics work automatically. Just start the SDK:

```go
client, _ := sdk.New(sdk.ClientConfig{Service: "api-server"})
client.Start(context.Background())
defer client.Stop()

// Runtime metrics are collected every 10s
// No further action needed!
```

## Understanding the Metrics

### Memory Metrics

#### `go_memstats_heap_alloc_bytes`
**Current heap allocation in bytes.**

- Shows how much memory your app is actively using
- Goes up when you allocate objects
- Goes down after GC runs
- **Use for:** Detecting memory growth trends

**Example:**
```
go_memstats_heap_alloc_bytes{service="api"} = 45,234,192  // ~45 MB
```

**What's normal?**
- Varies widely by app
- Look for steady state after warmup
- Alert if continuously growing (memory leak!)

#### `go_memstats_heap_sys_bytes`
**Total heap memory obtained from OS.**

- Total memory Go runtime has requested
- Always >= `heap_alloc_bytes`
- Doesn't shrink (Go keeps memory for reuse)
- **Use for:** Understanding max memory footprint

**Example:**
```
go_memstats_heap_sys_bytes{service="api"} = 67,108,864  // ~67 MB
```

**Why higher than heap_alloc?**
Go pre-allocates memory for future use. This is normal and efficient.

#### `go_memstats_heap_objects`
**Number of allocated objects on the heap.**

- Higher count = more GC work
- Rapidly changing = high allocation rate
- **Use for:** Detecting allocation-heavy code paths

**Example:**
```
go_memstats_heap_objects{service="api"} = 123,456
```

**What's high?**
- >1M objects can slow GC
- Look for spikes during high load

### Goroutine Metrics

#### `go_goroutines`
**Current number of goroutines.**

- Starts at ~10 for most apps (runtime internals)
- Grows with concurrent work
- **Use for:** Detecting goroutine leaks

**Example:**
```
go_goroutines{service="api"} = 42
```

**Warning signs:**
- Continuously growing (goroutine leak!)
- Spikes to thousands (connection leak?)
- Drops to zero (app crashed?)

**Common causes of goroutine leaks:**
```go
// ❌ BAD - Goroutine never exits
go func() {
    for {
        doWork()  // No exit condition!
    }
}()

// ✅ GOOD - Goroutine exits on context cancel
go func() {
    for {
        select {
        case <-ctx.Done():
            return  // Exit goroutine
        default:
            doWork()
        }
    }
}()
```

### Garbage Collection Metrics

#### `go_gc_cycles_total`
**Total number of GC cycles completed.**

- Increases over time
- More frequent GC = higher allocation rate
- **Use for:** Calculating GC frequency

**Example:**
```
go_gc_cycles_total{service="api"} = 1547
```

**Derive GC frequency:**
```
rate(go_gc_cycles_total[5m])  // GC cycles per second
```

**What's normal?**
- Low traffic: <1 GC/sec
- High traffic: 1-10 GC/sec
- >50 GC/sec: Probably allocating too much

#### `go_memstats_gc_duration_seconds`
**Cumulative GC pause time in seconds.**

- Total time app was paused for GC
- Includes all GC cycles
- **Use for:** Understanding GC impact on latency

**Example:**
```
go_memstats_gc_duration_seconds{service="api"} = 2.34  // 2.34s total
```

**Derive average pause time:**
```
go_memstats_gc_duration_seconds / go_gc_cycles_total
```

**What's acceptable?**
- <1ms per GC: Excellent
- 1-10ms: Normal for most apps
- >100ms: Problem! (reduce heap size or allocation rate)

## Use Cases

### Detect Memory Leaks

Watch `heap_alloc_bytes` over time:

```
Query: go_memstats_heap_alloc_bytes{service="my-app"}
```

**Healthy pattern:** Sawtooth (goes up, GC brings it down, repeat)
```
MB
100 ─     ╱╲    ╱╲    ╱╲
 80 ─    ╱  ╲  ╱  ╲  ╱  ╲
 60 ─   ╱    ╲╱    ╲╱    ╲
     └─────────────────────── Time
```

**Memory leak:** Continuously rising
```
MB
100 ─              ╱╱╱
 80 ─        ╱╱╱╱╱╱
 60 ─  ╱╱╱╱╱╱
     └─────────────────────── Time
```

### Detect Goroutine Leaks

Watch `go_goroutines` over time:

**Healthy:**
```
Count
100 ─ ━━━━━━━━━━━━━  (stable)
     └─────────────────────── Time
```

**Goroutine leak:**
```
Count
500 ─              ╱╱╱
300 ─        ╱╱╱╱╱╱
100 ─  ╱╱╱╱╱╱
     └─────────────────────── Time
```

### Monitor GC Pressure

Track GC frequency:

```
Query: rate(go_gc_cycles_total[5m])

Result: 3.2 GC/sec
```

**Interpretation:**
- <1 GC/sec: Low pressure, plenty of headroom
- 1-10 GC/sec: Moderate, typical for busy apps
- >20 GC/sec: High pressure, consider optimizing allocations

### Optimize Memory Usage

Compare heap allocation to heap system:

```
go_memstats_heap_alloc_bytes{service="api"} = 50 MB
go_memstats_heap_sys_bytes{service="api"} = 200 MB
```

**Interpretation:** App is using 50 MB but runtime reserved 200 MB. This is fine! Go manages memory efficiently.

## Collection Frequency

Runtime metrics are collected **every 10 seconds** (hardcoded):

```go
ticker := time.NewTicker(10 * time.Second)
```

**Why 10 seconds?**
- More frequent: Wastes CPU on `ReadMemStats()` (relatively expensive)
- Less frequent: Miss short-term memory spikes
- 10s is a good balance for most apps

**Want to change it?** You'd need to modify `sdk/client.go`:
```go
// Current:
ticker := time.NewTicker(10 * time.Second)

// For more frequent:
ticker := time.NewTicker(5 * time.Second)
```

## Performance Impact

### CPU Overhead

`runtime.ReadMemStats()` costs:
- ~50-200μs per call
- Called every 10s
- **Total overhead: <0.001% CPU**

Negligible for all practical purposes.

### Memory Overhead

Each metric sample is ~100 bytes:
- 6 metrics × 100 bytes = 600 bytes per collection
- Collected every 10s = 3.6 KB/minute
- **Total: ~5 MB/day**

Negligible.

## Comparison to Other Systems

| System | Runtime Metrics | Collection Method |
|--------|----------------|-------------------|
| TinyObs | ✅ Automatic | `runtime.ReadMemStats()` |
| Prometheus | ✅ Via exporter | Scrape `/metrics` endpoint |
| Datadog | ✅ Via agent | Agent polls runtime |
| New Relic | ✅ Via agent | Agent + profiler |

TinyObs makes it easier - no separate exporter or agent needed.

## Debugging with Runtime Metrics

### Scenario 1: Memory Leak

**Symptom:** `heap_alloc_bytes` keeps growing
```bash
curl "http://localhost:8080/v1/query?metric=go_memstats_heap_alloc_bytes"
```

**Diagnosis:**
1. Profile heap allocations: `pprof heap`
2. Look for goroutines holding references
3. Check for global caches without eviction

### Scenario 2: Goroutine Leak

**Symptom:** `go_goroutines` keeps growing
```bash
curl "http://localhost:8080/v1/query?metric=go_goroutines"
```

**Diagnosis:**
1. Profile goroutines: `pprof goroutine`
2. Look for goroutines blocked on channels
3. Check for missing context cancellation

### Scenario 3: High GC Pressure

**Symptom:** Frequent GC cycles, high pause times
```bash
curl "http://localhost:8080/v1/query?metric=go_gc_cycles_total"
```

**Diagnosis:**
1. Profile allocations: `pprof allocs`
2. Look for allocation-heavy loops
3. Consider object pooling (`sync.Pool`)

## Custom Runtime Collectors

Want to track additional runtime stats? Create your own collector:

```go
type CustomCollector struct {
    service string
}

func (c *CustomCollector) Collect(ctx context.Context) []metrics.Metric {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)

    return []metrics.Metric{
        {
            Name:  "go_memstats_stack_inuse_bytes",
            Type:  metrics.GaugeType,
            Value: float64(m.StackInuse),
            Labels: map[string]string{"service": c.service},
            Timestamp: time.Now(),
        },
        // Add more custom metrics...
    }
}

// Register with SDK
client.collectors = append(client.collectors, &CustomCollector{service: "my-app"})
```

## Further Reading

- [Go runtime package docs](https://pkg.go.dev/runtime)
- [A Guide to the Go GC](https://tip.golang.org/doc/gc-guide)
- [Go Memory Model](https://go.dev/ref/mem)

## Test Coverage

Coverage: **0.0%** (as of v2.2)

**This package needs tests!** Contributions welcome.

Suggested test cases:
- Collector returns 6 metrics
- All metrics have correct types
- Service label is set correctly

## See Also

- `pkg/sdk/` - Main SDK client that uses runtime collector
- `pkg/sdk/metrics/` - Gauge and Counter types used by collector
