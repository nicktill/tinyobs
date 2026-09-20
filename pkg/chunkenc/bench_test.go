package chunkenc

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// bytesPerSample fills a chunk from gen and reports the encoded cost per sample.
func bytesPerSample(t testing.TB, n int, gen func(i int) (int64, float64)) float64 {
	t.Helper()

	c := NewXORChunk()
	app, err := c.Appender()
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		ts, v := gen(i)
		require.NoError(t, app.Append(ts, v))
	}
	return float64(len(c.Bytes())) / float64(n)
}

// TestCompressionRatio pins the numbers published in the README.
//
// The baseline to beat is 16 bytes per sample: an int64 timestamp plus a
// float64 value, with no compression and no per-sample label repetition. The
// storage engine TinyObs shipped before this package measured 55.6 bytes per
// sample on disk, because every point carried a JSON copy of its own label set.
func TestCompressionRatio(t *testing.T) {
	const n = MaxSamplesPerChunk
	const base = int64(1700000000000)
	const step = int64(15000) // a 15s scrape interval

	rnd := rand.New(rand.NewSource(1))

	cases := []struct {
		name   string
		maxBPS float64
		gen    func(i int) (int64, float64)
	}{
		{
			// The single most common shape in a real system: a gauge that does
			// not move between scrapes.
			name:   "constant gauge, fixed interval",
			maxBPS: 0.5,
			gen:    func(i int) (int64, float64) { return base + int64(i)*step, 1 },
		},
		{
			// Counters climb by a steady amount, so both the timestamp and the
			// value deltas are highly regular.
			name:   "monotonic counter, fixed interval",
			maxBPS: 2.5,
			gen:    func(i int) (int64, float64) { return base + int64(i)*step, float64(i) * 7 },
		},
		{
			// A gauge wandering by small amounts: the XOR keeps a stable
			// leading/trailing window so most samples reuse it.
			name:   "slow-moving gauge, fixed interval",
			maxBPS: 8,
			gen: func(i int) (int64, float64) {
				return base + int64(i)*step, 100 + float64(i%7)*0.25
			},
		},
		{
			// Scrape jitter plus a noisy value. This is close to the worst
			// realistic case and still has to beat the uncompressed baseline.
			name:   "noisy gauge, jittered interval",
			maxBPS: 14,
			gen: func(i int) (int64, float64) {
				return base + int64(i)*step + int64(rnd.Intn(400)) - 200, rnd.Float64() * 1000
			},
		},
	}

	fmt.Printf("\n%-38s %14s %12s\n", "series shape", "bytes/sample", "vs 16B raw")
	fmt.Printf("%s\n", "-------------------------------------------------------------------")
	for _, tc := range cases {
		bps := bytesPerSample(t, n, tc.gen)
		fmt.Printf("%-38s %14.2f %11.1fx\n", tc.name, bps, 16/bps)
		require.LessOrEqual(t, bps, tc.maxBPS,
			"%s regressed: %.2f bytes/sample exceeds budget %.2f", tc.name, bps, tc.maxBPS)
	}
	fmt.Println()
}

func BenchmarkXORChunk_Append(b *testing.B) {
	const base = int64(1700000000000)
	const step = int64(15000)

	b.ReportAllocs()
	b.ResetTimer()

	appended := 0
	c := NewXORChunk()
	app, _ := c.Appender()

	for i := 0; i < b.N; i++ {
		if appended == MaxSamplesPerChunk {
			c = NewXORChunk()
			app, _ = c.Appender()
			appended = 0
		}
		_ = app.Append(base+int64(appended)*step, float64(i)*1.5)
		appended++
	}
}

func BenchmarkXORChunk_Iterate(b *testing.B) {
	const base = int64(1700000000000)
	const step = int64(15000)

	c := NewXORChunk()
	app, _ := c.Appender()
	for i := 0; i < MaxSamplesPerChunk; i++ {
		_ = app.Append(base+int64(i)*step, float64(i)*1.5)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		it := c.Iterator()
		for it.Next() {
			_, _ = it.At()
		}
	}
}
