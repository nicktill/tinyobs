package chunkenc

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

type sample struct {
	t int64
	v float64
}

// roundTrip appends every sample, reads them all back, and asserts they are
// bit-identical. Float comparison is exact on purpose: XOR encoding is lossless,
// and an approximate assertion here would hide precisely the bugs that matter.
func roundTrip(t *testing.T, samples []sample) *XORChunk {
	t.Helper()

	c := NewXORChunk()
	app, err := c.Appender()
	require.NoError(t, err)

	for _, s := range samples {
		require.NoError(t, app.Append(s.t, s.v))
	}
	require.Equal(t, len(samples), c.NumSamples())

	it := c.Iterator()
	var got []sample
	for it.Next() {
		ts, v := it.At()
		got = append(got, sample{ts, v})
	}
	require.NoError(t, it.Err())
	require.Len(t, got, len(samples))

	for i, want := range samples {
		require.Equal(t, want.t, got[i].t, "timestamp mismatch at index %d", i)
		if math.IsNaN(want.v) {
			require.True(t, math.IsNaN(got[i].v), "expected NaN at index %d", i)
			continue
		}
		require.Equal(t, math.Float64bits(want.v), math.Float64bits(got[i].v),
			"value mismatch at index %d: want %v got %v", i, want.v, got[i].v)
	}
	return c
}

func TestEmptyChunk(t *testing.T) {
	c := NewXORChunk()
	require.Equal(t, 0, c.NumSamples())

	it := c.Iterator()
	require.False(t, it.Next())
	require.NoError(t, it.Err())
}

func TestSingleSample(t *testing.T) {
	roundTrip(t, []sample{{1700000000000, 42.5}})
}

func TestRegularInterval(t *testing.T) {
	// The case the encoding is built for: a fixed scrape interval means every
	// delta-of-delta is zero.
	var samples []sample
	ts := int64(1700000000000)
	for i := 0; i < 100; i++ {
		samples = append(samples, sample{ts, float64(i)})
		ts += 15000
	}
	roundTrip(t, samples)
}

func TestConstantValue(t *testing.T) {
	// An unchanged value must cost a single bit, so a constant series should be
	// dramatically smaller than an 8-byte-per-sample baseline.
	var samples []sample
	ts := int64(1700000000000)
	for i := 0; i < 120; i++ {
		samples = append(samples, sample{ts, 1.0})
		ts += 15000
	}
	c := roundTrip(t, samples)
	require.Less(t, len(c.Bytes()), 60,
		"a constant series should compress to near nothing, got %d bytes", len(c.Bytes()))
}

func TestJitteredInterval(t *testing.T) {
	// Real scrapes drift. Each delta-of-delta should land in a narrow bucket
	// rather than the 64-bit escape.
	rnd := rand.New(rand.NewSource(7))
	var samples []sample
	ts := int64(1700000000000)
	for i := 0; i < 120; i++ {
		samples = append(samples, sample{ts, 100 + rnd.Float64()})
		ts += 15000 + int64(rnd.Intn(400)) - 200
	}
	roundTrip(t, samples)
}

func TestSpecialFloats(t *testing.T) {
	roundTrip(t, []sample{
		{1000, 0},
		{2000, math.Copysign(0, -1)},
		{3000, math.Inf(1)},
		{4000, math.Inf(-1)},
		{5000, math.NaN()},
		{6000, math.SmallestNonzeroFloat64},
		{7000, math.MaxFloat64},
		{8000, -math.MaxFloat64},
	})
}

func TestLargeAndNegativeDeltas(t *testing.T) {
	// Exercises every delta-of-delta bucket including the escape hatch, and
	// timestamps that go backwards relative to the previous delta.
	roundTrip(t, []sample{
		{0, 1},
		{1, 2},
		{100, 3},
		{10000, 4},
		{10001, 5},
		{1000000, 6},
		{1000000000000, 7},
		{1000000000001, 8},
	})
}

func TestBucketBoundaries(t *testing.T) {
	// Values chosen to sit exactly on the inclusive edges of each bucket.
	for _, width := range []uint8{14, 17, 20} {
		hi := int64(1) << (width - 1)
		lo := -(hi - 1)
		for _, dod := range []int64{lo, lo - 1, hi, hi + 1, 0, 1, -1} {
			base := int64(1700000000000)
			step := int64(15000)
			samples := []sample{
				{base, 1},
				{base + step, 2},
				{base + 2*step + dod, 3},
			}
			roundTrip(t, samples)
		}
	}
}

func TestChunkFull(t *testing.T) {
	c := NewXORChunk()
	app, err := c.Appender()
	require.NoError(t, err)

	ts := int64(0)
	for i := 0; i < MaxSamplesPerChunk; i++ {
		require.NoError(t, app.Append(ts, float64(i)))
		ts += 15000
	}
	require.Equal(t, MaxSamplesPerChunk, c.NumSamples())
	require.ErrorIs(t, app.Append(ts, 1), ErrChunkFull)
	require.Equal(t, MaxSamplesPerChunk, c.NumSamples(), "a rejected append must not change the chunk")
}

func TestFromBytesIsReadOnly(t *testing.T) {
	c := NewXORChunk()
	app, err := c.Appender()
	require.NoError(t, err)
	require.NoError(t, app.Append(1000, 1))
	require.NoError(t, app.Append(2000, 2))

	encoded := append([]byte(nil), c.Bytes()...)

	decoded, err := FromBytes(encoded)
	require.NoError(t, err)
	require.Equal(t, 2, decoded.NumSamples())

	it := decoded.Iterator()
	require.True(t, it.Next())
	ts, v := it.At()
	require.Equal(t, int64(1000), ts)
	require.Equal(t, 1.0, v)

	_, err = decoded.Appender()
	require.ErrorIs(t, err, ErrReadOnly)
}

func TestFromBytesRejectsShortBuffer(t *testing.T) {
	_, err := FromBytes([]byte{0x00})
	require.Error(t, err)
}

func TestAppenderResumesMidChunk(t *testing.T) {
	// Appender() replays the chunk to recover encoder state. Appending through
	// two separately-obtained appenders must produce the same bytes as one.
	build := func(split bool) []byte {
		c := NewXORChunk()
		app, err := c.Appender()
		require.NoError(t, err)

		ts := int64(1700000000000)
		for i := 0; i < 30; i++ {
			require.NoError(t, app.Append(ts, float64(i)*1.1))
			ts += 15000
			if split && i == 14 {
				app, err = c.Appender()
				require.NoError(t, err)
			}
		}
		return append([]byte(nil), c.Bytes()...)
	}
	require.Equal(t, build(false), build(true))
}

// TestRandomRoundTrip is a property test over shapes of data the encoder is
// likely to meet: counters that only climb, gauges that wander, and values that
// sit still for long stretches.
func TestRandomRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))

	for iter := 0; iter < 200; iter++ {
		n := 1 + rnd.Intn(MaxSamplesPerChunk)
		samples := make([]sample, 0, n)
		ts := rnd.Int63n(1 << 40)
		v := rnd.Float64() * 1000

		shape := rnd.Intn(3)
		for i := 0; i < n; i++ {
			samples = append(samples, sample{ts, v})
			ts += int64(rnd.Intn(60000) + 1)
			switch shape {
			case 0: // monotonic counter
				v += rnd.Float64() * 10
			case 1: // wandering gauge
				v += rnd.NormFloat64()
			case 2: // mostly flat
				if rnd.Intn(10) == 0 {
					v += rnd.Float64()
				}
			}
		}
		roundTrip(t, samples)
	}
}

func TestIteratorIsRepeatable(t *testing.T) {
	var samples []sample
	ts := int64(1700000000000)
	for i := 0; i < 50; i++ {
		samples = append(samples, sample{ts, float64(i) * 3.7})
		ts += 15000
	}
	c := roundTrip(t, samples)

	// A second independent iterator must see the same thing.
	it := c.Iterator()
	count := 0
	for it.Next() {
		count++
	}
	require.NoError(t, it.Err())
	require.Equal(t, len(samples), count)
}

func TestTruncatedStreamReportsError(t *testing.T) {
	c := NewXORChunk()
	app, err := c.Appender()
	require.NoError(t, err)
	ts := int64(1700000000000)
	for i := 0; i < 20; i++ {
		require.NoError(t, app.Append(ts, float64(i)*1.7))
		ts += 15000
	}

	full := c.Bytes()
	// Keep the header and sample count but lose most of the payload.
	truncated := append([]byte(nil), full[:headerLen+4]...)

	decoded, err := FromBytes(truncated)
	require.NoError(t, err)

	it := decoded.Iterator()
	for it.Next() {
	}
	require.Error(t, it.Err(), "iterating a truncated chunk must surface an error, not silently stop")
}
