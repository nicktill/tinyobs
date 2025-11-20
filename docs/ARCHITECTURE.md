# TinyObs Architecture

## Push vs Pull Model

### TL;DR
- **Prometheus**: Scrapes metrics FROM services (pull model)
- **TinyObs**: Services push metrics TO TinyObs (push model, like Datadog/New Relic)

### Detailed Comparison

#### Prometheus Model (Pull/Scrape)
```
┌─────────────┐           ┌──────────────┐
│   Service   │◄─────────┤  Prometheus  │
│             │  scrape   │   Server     │
│  /metrics   │           │              │
└─────────────┘           └──────────────┘
```

**How it works:**
1. Your service exposes a `/metrics` endpoint
2. Prometheus periodically scrapes (pulls) from that endpoint
3. Service doesn't need to know about Prometheus

**Pros:**
- Service discovery built-in
- Services don't need to know monitoring endpoint
- Easy to debug (just curl /metrics)

**Cons:**
- Prometheus needs network access to all services
- Harder with dynamic/ephemeral services (containers, serverless)
- Firewall/NAT complications

#### TinyObs Model (Push)
```
┌─────────────┐           ┌──────────────┐
│   Service   │──────────►│   TinyObs    │
│             │   push    │    Server    │
│  with SDK   │           │              │
└─────────────┘           └──────────────┘
```

**How it works:**
1. Your service uses the TinyObs SDK
2. SDK batches metrics and pushes them to TinyObs server
3. Service needs to know TinyObs endpoint

**Pros:**
- Works with ephemeral services (containers, serverless)
- No firewall/NAT issues
- Service controls what to send
- Better for dynamic environments

**Cons:**
- Services need configuration (TinyObs endpoint)
- Lost metrics if network fails (unless SDK queues)
- Services need SDK integration

## Why TinyObs Uses Push Model

TinyObs is designed for **local development and learning**, where:
- Services are short-lived (restart often)
- You control both the service and monitoring
- Simple integration is more important than discoverability
- Similar to production tools (Datadog, New Relic, AppDynamics)

## What About TinyObs's `/metrics` Endpoint?

TinyObs exposes a `/metrics` endpoint in Prometheus format. This endpoint exposes **all metrics that have been pushed to TinyObs**, making them available for external tools to scrape.

**Use case:**
If you want to visualize TinyObs metrics in Prometheus/Grafana:

```
┌─────────────┐     push      ┌──────────────┐
│ Your Service│──────────────►│   TinyObs    │
│  (with SDK) │               │   Server     │
└─────────────┘               │              │
                              │  /metrics    │◄───┐
                              └──────────────┘    │
                                                  │ scrape
                                           ┌──────────────┐
                                           │  Prometheus  │
                                           │   (optional) │
                                           └──────────────┘
```

## Integration Examples

### Wrong Expectation (Prometheus-style)
```go
// This is NOT how TinyObs works
func main() {
    // Expose /metrics endpoint
    http.HandleFunc("/metrics", promhttp.Handler())
    http.ListenAndServe(":8080", nil)
}

// Expecting TinyObs to scrape this - IT WON'T
```

### Correct Usage (TinyObs SDK)
```go
// This IS how TinyObs works
func main() {
    // Create TinyObs client
    client, _ := sdk.New(sdk.ClientConfig{
        Service:  "my-app",
        Endpoint: "http://localhost:8080/v1/ingest",
    })

    client.Start(context.Background())
    defer client.Stop()

    // Metrics are automatically pushed to TinyObs
    counter := client.Counter("requests_total")
    counter.Inc("endpoint", "/api/users")
}
```

## Summary

| Aspect | Prometheus | TinyObs |
|--------|-----------|---------|
| **Model** | Pull (scrape) | Push (send) |
| **Integration** | Expose /metrics | Use SDK |
| **Best For** | Production scale | Local dev |
| **Similar To** | VictoriaMetrics | Datadog, New Relic |

**Bottom Line:** TinyObs uses a push model (like Datadog) to keep it simple and educational.
