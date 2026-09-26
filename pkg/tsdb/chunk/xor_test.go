package chunk

import (
	"math"
	"math/rand"
	"testing"
)

type sample struct {
	t int64
	v float64
}

func encode(t *testing.T, samples []sample) *Chunk {
	t.Helper()
	c := New()
	app, err := c.Appender()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		app.Append(s.t, s.v)
	}
	return c
}

func decode(t *testing.T, c *Chunk) []sample {
	t.Helper()
	var out []sample
	it := c.Iterator()
	for it.Next() {
		ts, v := it.At()
		out = append(out, sample{ts, v})
	}
	if err := it.Err(); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func equalSamples(a, b []sample) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].t != b[i].t || math.Float64bits(a[i].v) != math.Float64bits(b[i].v) {
			return false
		}
	}
	return true
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	cases := map[string][]sample{}

	var regular, jittered, random, special []sample
	ts := int64(1_700_000_000_000)
	for i := 0; i < 120; i++ {
		regular = append(regular, sample{ts + int64(i)*15000, float64(i)})
	}
	ts2 := ts
	for i := 0; i < 120; i++ {
		ts2 += 15000 + rng.Int63n(2000) - 1000
		jittered = append(jittered, sample{ts2, 100 + rng.Float64()})
	}
	ts3 := ts
	for i := 0; i < 120; i++ {
		// Large, irregular gaps exercise every delta-of-delta width.
		ts3 += 1 + rng.Int63n(1<<uint(rng.Intn(40)))
		random = append(random, sample{ts3, rng.NormFloat64() * 1e6})
	}
	for i, v := range []float64{0, -0, 1, math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64, math.SmallestNonzeroFloat64, -1.5, 1.5} {
		special = append(special, sample{ts + int64(i)*1000, v})
	}

	cases["single"] = []sample{{ts, 42}}
	cases["two"] = []sample{{ts, 1}, {ts + 10, 2}}
	cases["regular"] = regular
	cases["jittered"] = jittered
	cases["random"] = random
	cases["special"] = special
	cases["negative_ts"] = []sample{{-5000, 1}, {-4000, 2}, {-1, 3}, {0, 4}, {9999999, 5}}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			c := encode(t, in)
			if got := decode(t, c); !equalSamples(got, in) {
				t.Fatalf("round trip mismatch:\n got %v\nwant %v", got, in)
			}
		})
	}
}

// Appending to a chunk that was persisted and reloaded must produce the same
// bytes as appending without the interruption.
func TestResumeAppend(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var all []sample
	ts := int64(1_700_000_000_000)
	for i := 0; i < 100; i++ {
		ts += 10000 + rng.Int63n(500)
		all = append(all, sample{ts, math.Round(rng.Float64()*1000) / 10})
	}
	want := encode(t, all)

	for split := 0; split <= len(all); split++ {
		c := encode(t, all[:split])
		reloaded, err := FromBytes(append([]byte(nil), c.Bytes()...))
		if err != nil {
			t.Fatal(err)
		}
		app, err := reloaded.Appender()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range all[split:] {
			app.Append(s.t, s.v)
		}
		if string(reloaded.Bytes()) != string(want.Bytes()) {
			t.Fatalf("split %d: resumed encoding differs from continuous encoding", split)
		}
		if got := decode(t, reloaded); !equalSamples(got, all) {
			t.Fatalf("split %d: decode mismatch", split)
		}
	}
}

func TestTruncatedChunkErrors(t *testing.T) {
	c := encode(t, []sample{{1000, 1}, {2000, 2}, {3000, 3.3}})
	b := c.Bytes()
	short, _ := FromBytes(b[:len(b)-2])
	it := short.Iterator()
	for it.Next() {
	}
	if it.Err() == nil {
		t.Fatal("expected error decoding truncated chunk")
	}
}

// A typical scrape: 15s interval with small jitter and a slowly rising counter.
func TestCompressionRatio(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var in []sample
	ts, v := int64(1_700_000_000_000), 0.0
	for i := 0; i < 120; i++ {
		ts += 15000 + rng.Int63n(20) - 10
		v += float64(rng.Intn(50))
		in = append(in, sample{ts, v})
	}
	c := encode(t, in)
	perSample := float64(len(c.Bytes())) / float64(len(in))
	t.Logf("%.2f bytes/sample", perSample)
	if perSample > 4 {
		t.Fatalf("compression regressed: %.2f bytes/sample", perSample)
	}
}

func BenchmarkAppend(b *testing.B) {
	for i := 0; i < b.N; i++ {
		c := New()
		app, _ := c.Appender()
		for j := int64(0); j < 120; j++ {
			app.Append(j*15000, float64(j))
		}
	}
}

func BenchmarkIterate(b *testing.B) {
	c := New()
	app, _ := c.Appender()
	for j := int64(0); j < 120; j++ {
		app.Append(j*15000, float64(j)*1.5)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it := c.Iterator()
		for it.Next() {
		}
	}
}
