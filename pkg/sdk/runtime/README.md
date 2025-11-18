# runtime

Automatic Go runtime metrics collection.

## Metrics Collected (every 10s)

| Metric | Description |
|--------|-------------|
| `go_memstats_heap_alloc_bytes` | Current heap memory |
| `go_memstats_heap_sys_bytes` | Total heap from OS |
| `go_memstats_heap_objects` | Allocated objects |
| `go_goroutines` | Current goroutines |
| `go_gc_cycles_total` | Total GC cycles |
| `go_memstats_gc_duration_seconds` | Cumulative GC pause |

## Usage

```go
client, _ := sdk.New(sdk.ClientConfig{Service: "my-app"})
client.Start(context.Background())
// Runtime metrics collected automatically every 10s
```

No configuration needed!

## Use Cases

### Detect Memory Leak

Watch `heap_alloc_bytes` - should be sawtooth (GC brings it down). Continuously rising = leak.

### Detect Goroutine Leak

Watch `go_goroutines` - stable is good. Growing = leak.

### Monitor GC Pressure

```
rate(go_gc_cycles_total[5m])  // GC frequency
```

- <1 GC/sec: Low pressure
- 1-10 GC/sec: Normal
- >20 GC/sec: High pressure, optimize allocations

## Performance Impact

`runtime.ReadMemStats()` costs ~50-200μs every 10s.

**Total overhead: <0.001% CPU**

## Test Coverage: 0.0%

Needs tests!
