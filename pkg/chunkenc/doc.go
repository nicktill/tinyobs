// Package chunkenc implements compressed storage for time series samples.
//
// A chunk holds up to MaxSamplesPerChunk (timestamp, float64) pairs for a
// single series, encoded with delta-of-delta timestamps and XOR-compressed
// values. The scheme is the one described in Facebook's Gorilla paper and used
// by Prometheus; this is an independent implementation of it.
//
// # Why this exists
//
// TinyObs previously stored one key/value pair per sample, with the value being
// a JSON document containing the metric name, type, value, timestamp and the
// full label set. Measured on 100,000 samples across 500 series with four
// labels each, that cost 55.6 bytes per sample on disk after BadgerDB's own
// compression.
//
// That number is the problem. At one million active series and a 15s scrape
// interval a system takes 5.76 billion samples a day, which at 55.6 bytes is
// about 320 GB/day. The same workload on Prometheus is roughly 9 GB.
//
// Two things drove the cost. Every sample re-sent its own label set even though
// the storage key already contained a hash identifying the series, and every
// sample paid for JSON's punctuation and its RFC 3339 timestamps. This package
// addresses the second; the series index introduced alongside it addresses the
// first by storing each label set exactly once.
//
// # How it encodes
//
// Timestamps use delta-of-delta. A scrape on a fixed interval produces a
// constant delta, so the second difference is exactly zero and costs one bit.
// Ordinary scheduler jitter lands in one of three progressively wider buckets
// (14, 17 and 20 bits); only a real change of interval falls through to a
// 64-bit escape. This is why the encoding rewards regular collection: the more
// boring your scrape schedule, the cheaper your storage.
//
// Values are XOR-ed against the preceding sample. Two floats of similar
// magnitude share their sign, exponent and most of their high mantissa bits, so
// the XOR is zero at both ends and only a window in the middle carries
// information. A sample whose value did not change at all costs one bit. When
// the meaningful window is unchanged from the previous sample the encoder
// reuses it and sends only the payload; otherwise it re-sends a 5-bit leading
// count and a 6-bit width first.
//
// # What it costs
//
// Measured by TestCompressionRatio at 120 samples per chunk, against a 16-byte
// baseline of one raw int64 and one raw float64:
//
//	constant gauge, fixed interval        0.42 bytes/sample     37.6x
//	monotonic counter, fixed interval     1.73 bytes/sample      9.2x
//	slow-moving gauge, fixed interval     0.95 bytes/sample     16.8x
//	noisy gauge, jittered interval        9.81 bytes/sample      1.6x
//
// The bottom row is the limit of the technique. Random values share no
// structure with their predecessors, so the XOR is dense and the encoder
// degrades to roughly the raw size plus a header.
//
// # What this deliberately does not do
//
// It does not support histograms, exemplars, or staleness markers. It handles
// float64 gauges and counters, which is what TinyObs ingests today. Adding
// native histogram support is a meaningfully harder problem and should be its
// own design, not an extension bolted onto this one.
//
// Chunks decoded via FromBytes are read-only. The number of unused bits in the
// final byte is not part of the encoded form, so appending to a decoded chunk
// would corrupt its last sample. Real TSDBs treat flushed chunks as immutable
// for the same reason, and new samples open a new chunk.
//
// Samples must arrive in non-decreasing timestamp order within a chunk. Out of
// order writes are a storage-engine concern, handled a layer up by cutting a
// new chunk rather than by making the encoder cope.
package chunkenc
