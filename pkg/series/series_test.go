package series

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
)

func TestFromMapIsSorted(t *testing.T) {
	ls := FromMap(map[string]string{"z": "1", "a": "2", "m": "3"})
	require.Equal(t, Labels{{"a", "2"}, {"m", "3"}, {"z", "1"}}, ls)
}

func TestFromMapEmpty(t *testing.T) {
	require.Nil(t, FromMap(nil))
	require.Nil(t, FromMap(map[string]string{}))
}

func TestHashIsOrderIndependent(t *testing.T) {
	// The same logical series built from maps in different orders must hash
	// identically, or a restart would fragment a series into two.
	a := FromMap(map[string]string{"service": "api", "method": "GET", "code": "200"})
	b := FromMap(map[string]string{"code": "200", "service": "api", "method": "GET"})
	require.Equal(t, a.Hash(), b.Hash())
	require.True(t, a.Equal(b))
}

// TestEncodingIsInjective checks that distinct label sets cannot encode to the
// same bytes. Under a delimiter-based scheme these pairs collide, because the
// delimiter can appear inside a label value.
func TestEncodingIsInjective(t *testing.T) {
	pairs := [][2]Labels{
		{
			Labels{{"a", "1"}, {"b", "2"}},
			Labels{{"a", "1\x00b\x002"}},
		},
		{
			Labels{{"a", "x"}, {"ab", "y"}},
			Labels{{"a", "xab"}, {"", "y"}},
		},
		{
			Labels{{"k", "v,k2=v2"}},
			Labels{{"k", "v"}, {"k2", "v2"}},
		},
		{
			Labels{{"a", ""}},
			Labels{{"", "a"}},
		},
		{
			Labels{{"a", "bc"}},
			Labels{{"ab", "c"}},
		},
	}

	for i, p := range pairs {
		t.Run(fmt.Sprintf("pair_%d", i), func(t *testing.T) {
			require.NotEqual(t, p[0].Encode(), p[1].Encode(), "encodings must differ")
			require.NotEqual(t, p[0].Hash(), p[1].Hash(), "hashes must differ")
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Labels{
		nil,
		{{"a", "1"}},
		{{"a", "1"}, {"b", "2"}, {"c", "3"}},
		{{"empty", ""}},
		{{"", "empty-name"}},
		{{"unicode", "héllo wörld 🎹"}},
		{{"nulls", "a\x00b"}},
		{{"long", string(make([]byte, 4096))}},
	}
	for i, ls := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			got, err := Decode(ls.Encode())
			require.NoError(t, err)
			require.True(t, ls.Equal(got), "want %s got %s", ls, got)
		})
	}
}

func TestDecodeRejectsTruncated(t *testing.T) {
	full := Labels{{"service", "api"}, {"method", "GET"}}.Encode()
	for n := 1; n < len(full); n++ {
		_, err := Decode(full[:n])
		require.Error(t, err, "truncating to %d bytes should fail", n)
	}
}

func TestDecodeRejectsEmpty(t *testing.T) {
	_, err := Decode(nil)
	require.Error(t, err)
}

func TestGetAndEqual(t *testing.T) {
	ls := FromMap(map[string]string{"service": "api", "code": "200"})

	v, ok := ls.Get("service")
	require.True(t, ok)
	require.Equal(t, "api", v)

	_, ok = ls.Get("missing")
	require.False(t, ok)

	require.False(t, ls.Equal(Labels{{"service", "api"}}))
	require.False(t, ls.Equal(Labels{{"service", "api"}, {"code", "500"}}))
}

func TestIndexAssignsStableIDs(t *testing.T) {
	ix := NewIndex()
	ls := FromMap(map[string]string{"service": "api"})

	id1, created := ix.GetOrCreate(ls)
	require.True(t, created)

	id2, created := ix.GetOrCreate(ls)
	require.False(t, created)
	require.Equal(t, id1, id2)

	require.Equal(t, 1, ix.Len())

	got, ok := ix.Labels(id1)
	require.True(t, ok)
	require.True(t, ls.Equal(got))
}

func TestIndexCopiesLabels(t *testing.T) {
	// A caller reusing its slice must not be able to mutate what the index
	// holds, or a series would change identity under it.
	ix := NewIndex()
	ls := Labels{{"service", "api"}}
	id, _ := ix.GetOrCreate(ls)

	ls[0].Value = "mutated"

	stored, ok := ix.Labels(id)
	require.True(t, ok)
	require.Equal(t, "api", stored[0].Value)
}

// TestIndexHandlesHashCollision forces the collision path by pre-seeding the
// index with a fabricated entry that occupies the ID a real series wants.
func TestIndexHandlesHashCollision(t *testing.T) {
	ix := NewIndex()

	realLabels := FromMap(map[string]string{"service": "api"})
	hash := realLabels.Hash()

	// Squat on the slot the real series would take, under the same hash bucket.
	imposter := Labels{{"different", "series"}}
	ix.byID[ID(hash)] = imposter
	ix.byHash[hash] = []ID{ID(hash)}

	id, created := ix.GetOrCreate(realLabels)
	require.True(t, created)
	require.NotEqual(t, ID(hash), id, "must not reuse the occupied ID")
	require.Equal(t, ID(hash+1), id, "should take the next free slot")

	// Both series must remain independently addressable.
	gotReal, ok := ix.Labels(id)
	require.True(t, ok)
	require.True(t, realLabels.Equal(gotReal))

	gotImposter, ok := ix.Labels(ID(hash))
	require.True(t, ok)
	require.True(t, imposter.Equal(gotImposter))

	// And lookup by labels must still find the right one.
	found, ok := ix.Get(realLabels)
	require.True(t, ok)
	require.Equal(t, id, found)
}

func TestIndexDelete(t *testing.T) {
	ix := NewIndex()
	ls := FromMap(map[string]string{"service": "api"})
	id, _ := ix.GetOrCreate(ls)

	ix.Delete(id)
	require.Equal(t, 0, ix.Len())

	_, ok := ix.Get(ls)
	require.False(t, ok)

	ix.Delete(id) // no-op, must not panic
	require.Equal(t, 0, ix.Len())

	// Re-creating gets a fresh ID from the same hash slot.
	id2, created := ix.GetOrCreate(ls)
	require.True(t, created)
	require.Equal(t, id, id2)
}

func TestIndexSnapshotLoadRoundTrip(t *testing.T) {
	ix := NewIndex()
	for i := 0; i < 100; i++ {
		ix.GetOrCreate(FromMap(map[string]string{
			"service":  fmt.Sprintf("svc-%d", i%7),
			"instance": fmt.Sprintf("10.0.0.%d", i),
		}))
	}
	snap := ix.Snapshot()
	require.Len(t, snap, 100)

	restored := NewIndex()
	require.NoError(t, restored.Load(snap))
	require.Equal(t, ix.Len(), restored.Len())

	// Every ID must resolve to the same labels, and every label set back to the
	// same ID. Storage keys reference IDs, so drift here orphans data.
	ix.ForEach(func(id ID, ls Labels) bool {
		got, ok := restored.Labels(id)
		require.True(t, ok)
		require.True(t, ls.Equal(got))

		gotID, ok := restored.Get(ls)
		require.True(t, ok)
		require.Equal(t, id, gotID)
		return true
	})
}

func TestIndexLoadRejectsDuplicateID(t *testing.T) {
	ix := NewIndex()
	err := ix.Load([]Entry{
		{ID: 1, Labels: Labels{{"a", "1"}}},
		{ID: 1, Labels: Labels{{"b", "2"}}},
	})
	require.Error(t, err)
}

func TestIndexLoadSortsLabels(t *testing.T) {
	// A persisted entry written by an older version might not be sorted.
	ix := NewIndex()
	require.NoError(t, ix.Load([]Entry{
		{ID: 42, Labels: Labels{{"z", "1"}, {"a", "2"}}},
	}))

	id, ok := ix.Get(FromMap(map[string]string{"a": "2", "z": "1"}))
	require.True(t, ok)
	require.Equal(t, ID(42), id)
}

func TestMatches(t *testing.T) {
	ls := FromMap(map[string]string{"service": "api", "code": "200", "method": "GET"})

	require.True(t, Matches(ls, nil))
	require.True(t, Matches(ls, Labels{{"service", "api"}}))
	require.True(t, Matches(ls, Labels{{"service", "api"}, {"code", "200"}}))
	require.False(t, Matches(ls, Labels{{"service", "web"}}))
	require.False(t, Matches(ls, Labels{{"missing", "x"}}))
}

func TestIndexConcurrentGetOrCreate(t *testing.T) {
	// The same series created from many goroutines must converge on one ID.
	ix := NewIndex()
	const goroutines = 32
	const series = 50

	var wg sync.WaitGroup
	ids := make([][]ID, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ids[g] = make([]ID, series)
			for i := 0; i < series; i++ {
				ls := FromMap(map[string]string{
					"service": fmt.Sprintf("svc-%d", i),
					"shard":   "a",
				})
				id, _ := ix.GetOrCreate(ls)
				ids[g][i] = id
			}
		}(g)
	}
	wg.Wait()

	require.Equal(t, series, ix.Len(), "concurrent creation must not duplicate series")
	for g := 1; g < goroutines; g++ {
		require.Equal(t, ids[0], ids[g], "all goroutines must agree on IDs")
	}
}

func TestNoCollisionsAcrossRealisticSeries(t *testing.T) {
	// Not a proof, but it catches an encoding that is accidentally lossy.
	ix := NewIndex()
	seen := make(map[uint64]string)
	rnd := rand.New(rand.NewSource(3))

	for i := 0; i < 20000; i++ {
		ls := FromMap(map[string]string{
			"__name__": fmt.Sprintf("metric_%d", rnd.Intn(50)),
			"service":  fmt.Sprintf("svc-%d", rnd.Intn(200)),
			"instance": fmt.Sprintf("10.%d.%d.%d", rnd.Intn(255), rnd.Intn(255), rnd.Intn(255)),
			"method":   []string{"GET", "POST", "PUT", "DELETE"}[rnd.Intn(4)],
		})
		h := ls.Hash()
		if prev, ok := seen[h]; ok {
			require.Equal(t, prev, ls.String(), "hash %d shared by two different label sets", h)
		}
		seen[h] = ls.String()
		ix.GetOrCreate(ls)
	}
	require.Equal(t, len(seen), ix.Len())
}

// TestHashMatchesEncode pins the streaming hash to the materialised encoding.
// If these ever diverge, series identity changes silently across a version
// bump and every existing series fragments in two.
func TestHashMatchesEncode(t *testing.T) {
	cases := []Labels{
		nil,
		{{"a", "1"}},
		{{"a", "1"}, {"b", "2"}, {"c", "3"}},
		{{"", ""}},
		{{"unicode", "héllo 🎹"}},
		{{"long", string(make([]byte, 300))}},
	}
	for i, ls := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			require.Equal(t, xxhash.Sum64(ls.Encode()), ls.Hash())
		})
	}
}

func TestHashDoesNotAllocate(t *testing.T) {
	ls := FromMap(map[string]string{
		"__name__": "http_requests_total",
		"service":  "api",
		"method":   "GET",
		"code":     "200",
	})
	avg := testing.AllocsPerRun(1000, func() { _ = ls.Hash() })
	require.Zero(t, avg, "Hash runs once per sample on ingest; it must not allocate")
}

func BenchmarkLabelsHash(b *testing.B) {
	ls := FromMap(map[string]string{
		"__name__": "http_requests_total",
		"service":  "api",
		"method":   "GET",
		"code":     "200",
		"instance": "10.0.0.1:9090",
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ls.Hash()
	}
}

func BenchmarkIndexGetOrCreate_Existing(b *testing.B) {
	ix := NewIndex()
	ls := FromMap(map[string]string{
		"__name__": "http_requests_total",
		"service":  "api",
		"method":   "GET",
		"code":     "200",
	})
	ix.GetOrCreate(ls)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ix.GetOrCreate(ls)
	}
}
