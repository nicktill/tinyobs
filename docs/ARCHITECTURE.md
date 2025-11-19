# TinyObs Architecture

This document explains how TinyObs works under the hood. The goal is to help you understand *why* things are built the way they are, not just *what* the code does.

## Table of Contents
1. [Data Flow](#data-flow)
2. [Storage & Downsampling](#storage--downsampling)
3. [Cardinality Protection](#cardinality-protection)
4. [Key Design Decisions](#key-design-decisions)

---

## Data Flow

TinyObs moves metrics from your application to persistent storage through several stages:

```
Your App → SDK → Batcher → HTTP Transport → Server → BadgerDB → Compaction → Dashboard
```

### Why Batching?

Sending each metric individually would create thousands of HTTP requests per second. The `Batcher` collects metrics in memory and sends them every 5 seconds. This reduces network overhead by ~100x.

**Critical: Unbounded Goroutine Protection**
Early versions had a bug: under high load, the batcher would spawn unlimited goroutines when flushing batches. If writes took 50ms and you wrote 100 metrics/sec, you'd spawn 5 goroutines/sec indefinitely.

The fix uses an atomic flag (`flushing`) to ensure only ONE flush runs at a time:

```go
// Only flush if no flush is already running
if shouldFlush && b.flushing.CompareAndSwap(false, true) {
    go func() {
        b.flush()
        b.flushing.Store(false)
    }()
}
```

**Why this matters:** Without this flag, a laptop running TinyObs for 53 days spawned so many goroutines it consumed 4.5 GB of RAM. This is production-critical.

### Why HTTP Transport?

We could use gRPC (faster), protocol buffers (smaller), or even raw TCP. But HTTP/JSON is:
- **Debuggable:** You can `curl` the API to see what's happening
- **Universal:** Works with any language, no code generation
- **Simple:** No protobuf schemas or gRPC setup

For a local dev tool, debuggability > performance.

---

## Storage & Downsampling

TinyObs uses BadgerDB (LSM tree + Snappy compression) for persistence. Here's why and how.

### Why BadgerDB?

**Alternatives considered:**
- **SQLite:** Great for queries, but slow for time-series writes (requires indexes)
- **In-memory:** Fast, but lost all data on restart (the 53-day bug)
- **Prometheus TSDB:** Perfect for metrics, but 50k+ lines of C++ (not learnable)

**BadgerDB wins because:**
1. **LSM trees are optimized for writes** - No index updates, just append
2. **Built-in compression** - Snappy reduces disk usage by ~3x
3. **Pure Go** - Easy to embed, no CGo dependencies
4. **Readable codebase** - You can actually understand how it works

### How Downsampling Works

Raw metrics take up tons of space. If you collect CPU usage every second for a year:
- 1 metric/sec × 86,400 sec/day × 365 days = **31.5 million data points**

Downsampling aggregates old data into larger buckets:

```
Raw (1s)  →  5min aggregates  →  1hr aggregates
   100%           4%                 0.4%
```

**Example:**
```
Raw (14 days max):
  2024-01-01 10:00:00  cpu=45.2
  2024-01-01 10:00:01  cpu=46.1
  2024-01-01 10:00:02  cpu=44.8
  ... (every second)

5-minute aggregate (90 days max):
  2024-01-01 10:00:00  cpu_avg=45.3, cpu_max=48.1, cpu_min=42.0

1-hour aggregate (1 year max):
  2024-01-01 10:00:00  cpu_avg=46.7, cpu_max=52.3, cpu_min=41.2
```

**Why 5min and 1hr?**
- **5min:** Detailed enough to see patterns (e.g., every-15-min cron job)
- **1hr:** Good for long-term trends (e.g., weekly traffic patterns)
- **Balance:** More resolutions = more storage. These two cover 90% of use cases.

**Retention Policy:**
- Raw: 14 days
- 5min: 90 days
- 1hr: 1 year

**Result:** 240x compression. A year of metrics takes ~70 MB instead of ~17 GB.

### How Aggregates Are Computed

The compaction engine runs hourly and computes:
- **Sum:** Total value across the window (for counters)
- **Count:** Number of data points
- **Min:** Lowest value
- **Max:** Highest value
- **Avg:** Sum / Count

**Why all five?**
Different metrics need different aggregates:
- Counters need **sum** (total requests)
- Gauges need **avg** (average CPU)
- Error tracking needs **max** (peak error rate)

We store all five so you can query what you need later.

---

## Cardinality Protection

**Cardinality = number of unique time series.**

Each unique combination of metric name + labels = one series:
```
http_requests{service="api", endpoint="/users", status="200"}  → series 1
http_requests{service="api", endpoint="/users", status="500"}  → series 2
http_requests{service="api", endpoint="/posts", status="200"}  → series 3
```

### Why Cardinality Matters

High cardinality kills performance. Here's why:

**Storage explosion:**
If you add a `user_id` label to every metric and have 10,000 users:
- 1 metric × 10,000 users = **10,000 series**
- 10,000 series × 86,400 points/day = **864 million data points/day**

That's 50+ GB/day. Your laptop runs out of disk in a week.

**Query slowdown:**
BadgerDB must scan ALL series matching a query. With 100k series, even simple queries take 10+ seconds.

**Memory overhead:**
Each series has metadata (labels, last write time, etc.). 100k series × 1 KB overhead = 100 MB just for metadata.

### How TinyObs Protects Against Cardinality Explosion

**1. Default limit: 10,000 series**
Configurable via `TINYOBS_MAX_CARDINALITY` environment variable.

**2. Rejection on write:**
If ingesting a new metric would exceed the limit, the server rejects it with HTTP 400:
```json
{
  "error": "cardinality limit exceeded",
  "current": 10000,
  "limit": 10000
}
```

**3. Why reject instead of drop silently?**
Silent drops are dangerous. You think metrics are being collected, but they're not. Rejections force you to fix the root cause (e.g., remove `user_id` label).

**Real example that triggered this:**
A dev added `request_id` (a UUID) as a label. Each request created a NEW series. In 10 minutes:
- 1,000 requests/min × 10 min = **10,000 series**
- Cardinality limit hit, server started rejecting
- Dev got error, realized the mistake, removed the UUID label

Without the limit, that would've created 1.4 million series/day and crashed the server.

---

## Key Design Decisions

### 1. Why No PromQL (Yet)?

PromQL is powerful but adds ~5,000 lines of parser/evaluator code. For v2, you can only query one metric at a time. Functions like `rate()` and `sum()` are planned for v4.

**Tradeoff:** Simplicity and learnability now > query power later.

### 2. Why Polling Instead of WebSockets?

The dashboard polls every 30 seconds. WebSockets would be real-time but add:
- Connection management (reconnects, timeouts)
- Server state (tracking connected clients)
- Backpressure handling (slow clients blocking fast writes)

**Tradeoff:** Polling is 100 lines of code. WebSockets would be 1,000+ lines. Polling is good enough for v2.

### 3. Why No Authentication?

TinyObs is designed for **local development only**. Adding auth means:
- API keys or OAuth
- User management
- Session handling
- HTTPS/TLS setup

That's another 2,000+ lines of code. For a tool running on `localhost`, it's overkill.

**If you want to run this in production,** you'll need to add a reverse proxy (nginx) with auth. TinyObs focuses on being a great local tool, not a SaaS.

### 4. Why BadgerDB Over Prometheus TSDB?

Prometheus TSDB is *the* gold standard for time-series storage. But:
- **Size:** 50k+ lines of C++ and Go
- **Complexity:** Custom compression (Gorilla algorithm), chunk encoding, mmap magic
- **Hard to learn:** Would take weeks to understand

BadgerDB is:
- **Readable:** ~10k lines of well-commented Go
- **Good enough:** LSM + Snappy gets 80% of the performance
- **Understandable:** You can read the source in a weekend

**Tradeoff:** 80% performance, 10x easier to understand.

---

## Performance Characteristics

**Write throughput:** ~50,000 metrics/sec (M1 MacBook Pro, BadgerDB)
**Storage efficiency:** ~240x compression (raw → 5m → 1h)
**Query latency:** <100ms for 1,000 points (in-memory), <500ms (BadgerDB)
**Memory usage:** ~50 MB baseline + ~5 KB per active series
**Disk usage:** ~70 MB after 1 year (10 series, 1 sample/sec)

---

## What's Next?

**V3.0:** WebSocket live updates, anomaly detection, threshold alerts
**V4.0:** PromQL-like query language with `rate()`, `sum()`, `avg()`
**V5.0:** Clustering, cloud storage (S3), multi-tenancy

Each version adds complexity. The goal is to keep TinyObs *understandable* even as it grows more powerful.

---

## Further Reading

- [Prometheus TSDB Design](https://github.com/prometheus/prometheus/blob/main/tsdb/docs/format/README.md)
- [Gorilla: Facebook's Time-Series Database](http://www.vldb.org/pvldb/vol8/p1816-teller.pdf)
- [LSM Trees Explained](https://www.igvita.com/2012/02/06/sstable-and-log-structured-storage-leveldb/)
- [BadgerDB Source Code](https://github.com/dgraph-io/badger)

---

**Questions?** Open an issue on GitHub or read the code. Every file has comments explaining *why*, not just *what*.
