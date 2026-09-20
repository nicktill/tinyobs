# ADR 0002: Millisecond timestamp resolution

Status: accepted
Date: 2026-09-20

## Context

TinyObs stored sample timestamps as `time.Time.UnixNano()`. The chunk encoding
introduced in [ADR 0001](0001-chunked-xor-storage.md) compresses timestamps with
delta-of-delta: a fixed collection interval produces a constant delta, so the
second difference is zero and costs a single bit.

That property is exactly as good in nanoseconds as in milliseconds for a
perfectly regular scrape. The difference shows up under jitter, and real
collection always jitters.

The delta-of-delta buckets are 14, 17 and 20 bits wide, holding roughly ±8,000,
±65,000 and ±524,000. In milliseconds those absorb 8 seconds, a minute and nine
minutes of drift respectively, which covers essentially all real scheduler
noise. In nanoseconds the widest bucket holds **524 microseconds**. A scrape
that lands 1ms late produces a delta-of-delta of 1,000,000, which overflows
every bucket and falls through to the 64-bit escape.

So with nanosecond timestamps, a normal jittery scrape costs 68 bits per sample
for the timestamp alone. With milliseconds it costs 16.

Widening the buckets to suit nanoseconds would defeat the point: the buckets are
narrow *because* narrow is cheap.

## Decision

Store sample timestamps as **milliseconds since the Unix epoch** (`int64`).

The `chunkenc` package itself stays unit-agnostic (it encodes whatever `int64`
it is handed), but its bucket widths are chosen for milliseconds, and the
storage engine converts at the boundary.

## Consequences

- Sub-millisecond timestamp precision is lost. Two samples for the same series
  within the same millisecond collapse to the same timestamp.
- Jittered collection costs 16 bits per timestamp instead of 68.
- The on-disk format matches Prometheus and the Prometheus remote-write protocol,
  which also use millisecond timestamps. Remote-write ingestion needs no
  conversion, and exported data lines up with what Grafana expects.
- The HTTP ingest API continues to accept RFC 3339 timestamps and converts on
  the way in, so no client changes are required.

## Why this is acceptable

Metrics are aggregates over a window, not events. A counter sampled at 15s
intervals carries no meaning at microsecond resolution. The timestamp records
roughly when the value was observed, and "roughly" is bounded by the scrape
interval, not by clock precision. Every widely deployed metrics system makes
this same choice: Prometheus, the remote-write protocol, and Grafana all work in
milliseconds.

Traces are the opposite case. A span's duration genuinely is the measurement,
and nanoseconds matter. If TinyObs later grows span storage it should not reuse
this decision; spans want their own encoding with their own resolution.
