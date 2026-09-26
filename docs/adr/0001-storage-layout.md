# ADR 0001: Storage layout

Status: accepted
Date: 2026-09-26

## Context

V1 stores one BadgerDB key per sample. The value is the JSON-encoded metric,
including the full label map and an RFC 3339 timestamp. That costs 47 bytes per
sample. Label filters are applied after scanning every series of the metric,
so reading one series over six hours takes about 650 ms.

TinyObs targets at most 10,000 active series on one machine, with 72 hours of
retention (see [the V2 design](../design/v2.md#workload-targets)). The storage
layout has to fit that comfortably with the least code that can be trusted.

## Measurements

Six designs were run against the same workload: 2,000 series, a 15 s interval,
6 hours, 2.88 million samples. The value mix is half counters, a third
two-decimal gauges and a fifth full-precision random floats. The random floats
are the worst case for XOR compression. Disk is measured after a full LSM
compaction, so superseded versions and tombstones don't distort the numbers.

| Design | Disk (B/sample) | Ingest (samples/s) | Startup | 1 series, 6 h | 100 series, 1 h |
|---|---|---|---|---|---|
| V1: KV per sample, JSON | 47.3 | 120k | 8 ms | 659 ms | 657 ms |
| KV per sample, binary, Snappy | 21.8 | 330k | 11 ms | 1.2 ms | 19 ms |
| **KV per sample, binary, ZSTD** | **13.6** | **336k** | **11 ms** | **1.4 ms** | **23 ms** |
| Chunks, uncompressed | 10.7 | 157k | 838 ms | 0.1 ms | 5.4 ms |
| Gorilla chunks, open chunk rewritten per commit | 7.0 (33.4 before compaction) | 218k | 2.5 s | 0.3 ms | 10 ms |
| WAL + Gorilla chunks written once | 8.0 | 179k | 413 ms | 0.2 ms | 9 ms |

The prototypes and the harness are in commit
[`8b7ef22`](https://github.com/nicktill/tinyobs/commit/8b7ef22). To reproduce:
`go test -tags storagebench -run TestStorageComparison -v -timeout 30m ./pkg/tsdb/`.

## Decision

Store one Badger key per sample:

```
d <series id:8> <timestamp ms:8, sign bit flipped>  →  float64 bits (8 bytes)
s <series id:8>                                      →  encoded label set
m <metric name>                                      →  JSON metadata
```

- Badger uses ZSTD block compression.
- Series IDs are derived from the label hash, with a linear probe on collision.
- The label index (postings) lives in memory and is rebuilt from `s` keys at
  startup.

## Consequences

- **Disk is 3.5× smaller than V1.**
  - Reading one series is ~470× faster, and reading a whole metric ~28×
    faster.
  - Ingest is 2.8× faster.
  - Startup doesn't grow with the amount of data stored.
  - At the target ceiling (10k series, 15 s interval, 72 h) disk is about
    2.4 GB. A typical 1k-series setup uses about 240 MB.
- **The storage code is roughly 150 lines plus the index.** Durability comes
  from Badger: a sample is on disk once its write batch is flushed. There is no
  head block to recover and no WAL to replay.
- **Compressed chunks would use ~40% less disk and query ~3× faster.** They
  would also mean ~4× the code, half the ingest rate, startup time that grows
  with series count, and a WAL whose recovery we would have to prove. We give
  those gains up for now.
  - Revisit if disk usage or query latency becomes a real complaint.
  - The storage API (append, select by matchers) doesn't expose the layout, so
    the change would be internal.
- **Chunks written only when full (PR #39) are not an option.** They lose up to
  a full chunk of samples per series on a crash.
- **Retention deletes keys per series in bounded batches.** Deletes leave
  tombstones until Badger compacts, so disk lags the retention window by up to
  one compaction cycle.
- **V1 data is not migrated.** V2 uses a new data directory.
