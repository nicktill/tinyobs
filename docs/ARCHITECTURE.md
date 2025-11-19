# TinyObs Architecture

This document explains how TinyObs works.

## Data Flow

Metrics flow through TinyObs like this:

```
Your App → SDK → Batcher → HTTP → Server → BadgerDB → Compaction → Dashboard
```

### 1. SDK (Client)

Your app creates metrics using the SDK:

```go
requests := client.Counter("http_requests_total")
requests.Inc("endpoint", "/api/users")
```

### 2. Batcher

The SDK batches metrics and sends them every 5 seconds instead of immediately. This reduces network overhead.

### 3. HTTP Transport

Metrics are sent to the server via HTTP POST to `/v1/ingest`.

### 4. Server

The server receives metrics and writes them to BadgerDB storage.

### 5. BadgerDB

BadgerDB is a key-value store that persists metrics to disk. It uses LSM trees for fast writes and Snappy compression to save space.

### 6. Compaction

Old metrics are aggregated into larger time buckets to save space:
- Raw data (every sample) → 5-minute aggregates
- 5-minute aggregates → 1-hour aggregates

### 7. Dashboard

The dashboard queries metrics via `/v1/query/range` and displays charts.

---

## Storage & Downsampling

Raw metrics take up a lot of space. TinyObs saves space by aggregating old data.

**Example:**
```
Raw (keep 14 days):
  10:00:00 cpu=45.2
  10:00:01 cpu=46.1
  10:00:02 cpu=44.8
  ... (every second)

5-minute aggregate (keep 90 days):
  10:00:00 cpu_avg=45.3, cpu_min=42.0, cpu_max=48.1

1-hour aggregate (keep 1 year):
  10:00:00 cpu_avg=46.7, cpu_min=41.2, cpu_max=52.3
```

This reduces storage by ~240x over time.

---

## Cardinality Protection

**Cardinality = number of unique time series.**

Each unique combination of metric name + labels creates a new series:

```
http_requests{service="api", endpoint="/users", status="200"}  → series 1
http_requests{service="api", endpoint="/users", status="500"}  → series 2
http_requests{service="api", endpoint="/posts", status="200"}  → series 3
```

### Why it matters

High cardinality causes problems:

**Bad example:**
```go
// DON'T DO THIS - creates a new series for every user!
requests.Inc("user_id", "user_12345", "endpoint", "/api")
// With 10,000 users → 10,000 series → lots of storage
```

**Good example:**
```go
// DO THIS - low cardinality
requests.Inc("endpoint", "/api", "status", "200")
// Only creates one series per endpoint/status combo
```

TinyObs limits you to 10,000 series by default to prevent storage explosion.

---

## Why BadgerDB?

We use BadgerDB because:
- It's written in Go (easy to embed)
- LSM trees are fast for writes
- Built-in compression saves disk space
- Easy to understand

Alternatives like Prometheus TSDB are faster but have 50k+ lines of code and are harder to learn.

---

## Key Design Choices

### 1. HTTP/JSON instead of gRPC

HTTP is easier to debug. You can `curl` the API to see what's happening.

### 2. Polling instead of WebSockets

The dashboard polls every 30 seconds. WebSockets would be real-time but add complexity.

### 3. No authentication

TinyObs is for local development on `localhost`. For production, use a reverse proxy with auth.

### 4. No PromQL (yet)

PromQL adds thousands of lines of parser code. TinyObs prioritizes simplicity.

---

## Performance

- Write throughput: ~50k metrics/sec
- Storage: ~240x compression after downsampling
- Query latency: <500ms for typical queries
- Memory: ~50 MB baseline

---

## Further Reading

- [Prometheus TSDB Design](https://github.com/prometheus/prometheus/blob/main/tsdb/docs/format/README.md)
- [BadgerDB Source](https://github.com/dgraph-io/badger)
