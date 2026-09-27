# metrics

Counter, Gauge and Histogram. Instruments only update memory; the client
reports the current value of every series once per flush interval (default
5s), stamped with one timestamp. Network traffic grows with the number of
series, never with request rate.

## Counter

```go
requests := client.Counter("requests_total")
requests.Inc("endpoint", "/users")
requests.Add(5, "endpoint", "/batch")
```

Cumulative. Negative values are ignored. Query with `rate()` or `increase()`.

## Gauge

```go
queue := client.Gauge("queue_size")
queue.Set(42)
queue.Inc()
queue.Sub(5)
```

## Histogram

```go
latency := client.Histogram("request_duration_seconds")
latency.Observe(0.034, "endpoint", "/users")
```

Exposed as a Prometheus classic histogram: cumulative `_bucket{le}` series
(including `+Inf`), `_sum` and `_count`. Default buckets run from 1ms to 10s.

```promql
histogram_quantile(0.99, sum by (le) (rate(request_duration_seconds_bucket[5m])))
```

## Labels

Alternating name/value pairs: `Inc("method", "GET", "status", "200")`. A
trailing name without a value is ignored. Every distinct combination is a
series, so never use unbounded values (user IDs, raw URLs) as labels.
