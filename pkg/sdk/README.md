# sdk

A small Go SDK for applications that have no metrics instrumentation yet. It
aggregates counters, gauges and histograms in memory and exports them to
TinyObs (or any OTLP/HTTP endpoint) every 10 seconds.

If your application already uses the Prometheus client library or the
OpenTelemetry SDK, you don't need this: scrape its `/metrics` endpoint or point
its OTLP exporter at TinyObs.

```go
client, err := sdk.New(sdk.ClientConfig{
    Service:  "checkout",              // the job label
    Endpoint: "http://localhost:8080", // TinyObs base URL or a full OTLP URL
})
if err != nil {
    log.Fatal(err)
}
client.Start(ctx)
defer client.Stop() // sends a final export

handler := httpx.Middleware(client)(mux) // http_requests_total, http_request_duration_seconds

orders := client.Counter("orders_total")
orders.Inc("region", "eu")
client.Gauge("queue_depth").Set(12)
client.Histogram("job_duration_seconds").Observe(0.42, "job", "resize")
```

Semantics match the Prometheus client library: counters and histogram
buckets are cumulative for the life of the process, labels are passed as
alternating name/value pairs, and `Stop` flushes before returning. Go runtime
gauges (`go_goroutines`, `go_memstats_*`) are exported alongside your metrics.
