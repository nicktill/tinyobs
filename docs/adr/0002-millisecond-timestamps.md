# ADR 0002: Millisecond timestamps

Status: accepted
Date: 2026-09-26 (first proposed 2026-09-20 in #40)

## Context

V1 stores sample timestamps as `time.Time.UnixNano()`. Every system TinyObs
interoperates with uses milliseconds:
- the Prometheus exposition format;
- the Prometheus HTTP API and PromQL evaluation;
- remote write;
- Grafana.

OTLP uses nanoseconds on the wire, but its Prometheus translation truncates
them to milliseconds.

## Decision

Samples carry an `int64` timestamp in **milliseconds since the Unix epoch**.
Each ingest path converts at its boundary: exposition timestamps are already in
milliseconds, and OTLP timestamps are divided by 10⁶.

## Consequences

- **Two samples for one series in the same millisecond collide.** The second is
  either a duplicate (same value) or rejected. Metrics are observations of
  aggregates at a scrape interval, so sub-millisecond precision carries no
  information.
- **Query evaluation matches Prometheus exactly.** Range boundaries, lookback
  and `timestamp()` all work in milliseconds, so the conformance tests compare
  like with like.
- **Traces would need nanoseconds**, because there the duration is the
  measurement. If TinyObs ever stores spans, they need their own decision.
  Traces are a V2 non-goal.
