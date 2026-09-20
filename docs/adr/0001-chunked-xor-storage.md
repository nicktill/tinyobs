# ADR 0001: Chunked, XOR-compressed sample storage

Status: accepted
Date: 2026-09-20

## Context

TinyObs stored one BadgerDB key/value pair per sample. The key was
`[nameLen][name][seriesHash][timestamp]` and the value was `json.Marshal` of the
full metric, including the complete label map and an RFC 3339 timestamp.

Measured on 100,000 samples across 500 series with four labels each, this cost
**55.6 bytes per sample** on disk after BadgerDB's own compression, at an ingest
rate of 588,000 samples/sec.

For scale: one million active series at a 15s scrape interval produces 5.76
billion samples a day. At 55.6 bytes that is roughly **320 GB/day**. The same
workload on Prometheus is about 9 GB.

Two separate costs were stacked here:

1. **Label duplication.** The key already contains a hash that identifies the
   series. Re-sending the label set with every sample is pure redundancy.
2. **Per-sample encoding overhead.** JSON pays for field names, punctuation and
   a 30-character timestamp on every data point.

The second is what this ADR addresses. The first is addressed by the series
index, which stores each label set once and is keyed by the same hash.

## Decision

Store samples in **chunks**: one key/value pair per `(series, time window)`,
holding up to 120 samples encoded with delta-of-delta timestamps and XOR-encoded
float values, per the Gorilla paper.

We implement the codec ourselves in `pkg/chunkenc` rather than depending on
`prometheus/prometheus`. That dependency pulls in a very large tree for one
small piece of it, and the encoding is a known, stable, well-documented
technique. Writing it makes the tradeoffs legible in this repository rather than
hidden behind an import.

### Why 120 samples per chunk

A chunk is the unit of read amplification: reading one sample means decoding the
whole chunk. 120 samples at a 15s interval is 30 minutes of data, which keeps
decode cost bounded (measured at roughly 17ns per sample) while amortising the
chunk header and the per-series key across enough points to matter. This is the
same bound Prometheus uses, for the same reason.

### Why not columnar or Parquet

Both would compress better for analytical scans, and both would make the write
path substantially more complex: buffering, row-group management, and a
compaction story that has to rewrite files rather than append. TinyObs is aimed
at a single binary running beside a small service, where operational simplicity
is the product. LSM plus chunks gets most of the compression benefit with an
append-only write path.

## Consequences

Measured at 120 samples per chunk against a 16-byte raw baseline:

| series shape                      | bytes/sample | vs raw | vs the 55.6B it replaces |
| --------------------------------- | -----------: | -----: | -----------------------: |
| constant gauge, fixed interval    |         0.42 |  37.6x |                     132x |
| monotonic counter, fixed interval |         1.73 |   9.2x |                      32x |
| slow-moving gauge, fixed interval |         0.95 |  16.8x |                      59x |
| noisy gauge, jittered interval    |         9.81 |   1.6x |                     5.7x |

Costs we are accepting:

- **Random-valued series barely compress.** The XOR is dense when consecutive
  values share no structure, and the encoder degrades to about the raw size plus
  a header. This is inherent, not a tuning problem.
- **Chunks are immutable once encoded.** The count of unused bits in the final
  byte is not part of the serialised form, so a decoded chunk cannot be appended
  to. New samples open a new chunk.
- **Samples must arrive in order within a chunk.** Out-of-order handling moves
  up to the storage engine, which cuts a new chunk rather than forcing the
  encoder to cope.
- **Point lookups get more expensive.** Reading one sample now decodes up to 119
  others. At 17ns per sample that is under 2 microseconds, which is well inside
  the noise of a BadgerDB read.
- **No histogram support.** Native histograms are a harder problem and deserve
  their own design rather than being bolted on here.

## Alternatives considered

**Keep one KV per sample but swap JSON for protobuf or msgpack.** Would have
cut maybe 60% of the value size while leaving both the per-key overhead and the
label duplication intact. Roughly 20 bytes/sample rather than 1 to 2. Not worth
a migration.

**Depend on `prometheus/tsdb`.** Better tested than anything written here, and
it brings a dependency footprint far larger than the rest of TinyObs combined,
along with assumptions about WAL layout and block structure that do not fit a
single-binary tool.

**Compress whole blocks with zstd.** Simple, and genuinely effective on JSON.
But it compresses the redundancy rather than removing it, needs whole-block
decompression to read one sample, and does nothing about label duplication.
