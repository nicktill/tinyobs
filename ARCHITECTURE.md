# TinyObs Architecture

## System Overview

```
                    ┌─────────────────────────────────────┐
                    │      Applications (with SDK)        │
                    │  ┌──────┐  ┌──────┐  ┌──────┐      │
                    │  │ App 1│  │ App 2│  │ App N│      │
                    │  └──┬───┘  └──┬───┘  └──┬───┘      │
                    │     │         │         │           │
                    │     └─────────┴─────────┘           │
                    └───────────────┬─────────────────────┘
                                    │
                            POST /v1/ingest
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │      Ingest Handler           │
                    │  • Validates metrics          │
                    │  • Cardinality checks         │
                    │  • Storage limit enforcement  │
                    └───────────────┬───────────────┘
                                    │
                    ┌───────────────┴───────────────┐
                    │                               │
                    ▼                               ▼
        ┌───────────────────────┐    ┌───────────────────────┐
        │    BadgerDB Storage    │    │   WebSocket Hub       │
        │                       │    │   (Real-time updates) │
        │  • Raw data           │    └───────────┬───────────┘
        │  • 5-min aggregates   │                │
        │  • 1-hour aggregates  │                │
        └───────────┬───────────┘                │
                    │                            │
                    │                            │
                    ▼                            ▼
        ┌───────────────────────┐    ┌───────────────────────┐
        │    Query Handler      │    │     Dashboard UI      │
        │                       │    │                       │
        │  • PromQL-like queries│    │  • Interactive charts │
        │  • Range queries      │    │  • Live metric updates│
        │  • Aggregations       │    │  • Query interface    │
        └───────────┬───────────┘    └───────────────────────┘
                    │
            GET /v1/query
                    │
                    ▼
        ┌───────────────────────┐
        │   Compaction Engine    │
        │   (Background job)    │
        │                       │
        │  • Downsampling       │
        │  • Data compression   │
        │  • Retention cleanup  │
        └───────────┬───────────┘
                    │
                    └───────────────┐
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │      Export Handler            │
                    │                               │
                    │  • JSON export                │
                    │  • CSV export                 │
                    │  • Backup/restore             │
                    └───────────────────────────────┘
```

### Components

- **SDK Client**: Batches and pushes metrics from applications
- **Ingest Handler**: Validates and stores incoming metrics
- **BadgerDB Storage**: Persistent time-series storage with compression
- **Query Handler**: Executes PromQL-like queries
- **Compaction Engine**: Downsamples old data (raw → 5m → 1h aggregates)
- **WebSocket Hub**: Broadcasts real-time metric updates
- **Dashboard UI**: Visualizes metrics and queries
- **Export Handler**: Backup/restore functionality

## Push vs Pull Model

TinyObs uses a **push model** (like Datadog/New Relic), not a pull model (like Prometheus).

### Comparison

| Aspect | Prometheus (Pull) | TinyObs (Push) |
|--------|------------------|----------------|
| **How it works** | Scrapes `/metrics` endpoints | Services push via SDK |
| **Service config** | None needed | Must know TinyObs endpoint |
| **Network access** | Prometheus needs access to all services | Services push outbound |
| **Best for** | Production, service discovery | Ephemeral services, containers |
| **Similar to** | VictoriaMetrics | Datadog, New Relic |

### Why Push?

TinyObs is designed for **local development and learning**:
- Services are short-lived (restart often)
- You control both service and monitoring
- Simple integration > discoverability
- Works with containers/serverless

## Data Flow

1. **Ingestion**: SDK batches metrics → POST `/v1/ingest` → Ingest Handler validates → BadgerDB stores
2. **Querying**: Dashboard/API → GET `/v1/query` → Query Handler → BadgerDB reads
3. **Compaction**: Background job → Compaction Engine → Downsamples old data → BadgerDB updates
4. **Real-time**: Ingest Handler → WebSocket Hub → Dashboard (live updates)
5. **Export**: BadgerDB → Export Handler → GET `/v1/export` → JSON/CSV download

## Storage & Compaction

TinyObs uses **multi-resolution downsampling** to reduce storage:

- **Raw data** (0-14 days): Full resolution, every sample
- **5-minute aggregates** (14-90 days): One aggregate per 5-minute window
- **1-hour aggregates** (90 days - 1 year): One aggregate per hour

This provides **240x compression** for old data while keeping recent data at full resolution.

## Key Design Decisions

1. **Push model**: Simpler for local dev, works with ephemeral services
2. **BadgerDB**: Embedded, no external dependencies, good for single-node
3. **Downsampling**: Automatic compression to manage storage growth
4. **WebSocket**: Real-time updates without polling
5. **PromQL-like queries**: Familiar query language for learning

## See Also

- [Quick Start Guide](../QUICK_START.md) - How to run and test
- [SDK Documentation](../pkg/sdk/doc.go) - Client library usage
- [Testing Guide](../TESTING.md) - How to test the system
