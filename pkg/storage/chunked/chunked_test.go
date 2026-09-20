package chunked

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
	badgerstore "github.com/nicktill/tinyobs/pkg/storage/badger"
)

func newTestStore(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(Config{Path: dir, MaxMemoryMB: 64})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func mkMetric(name string, v float64, ts time.Time, labels map[string]string) metrics.Metric {
	return metrics.Metric{
		Name:      name,
		Type:      metrics.MetricType("gauge"),
		Value:     v,
		Labels:    labels,
		Timestamp: ts,
	}
}

func TestWriteQueryRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Millisecond)
	var in []metrics.Metric
	for i := 0; i < 50; i++ {
		in = append(in, mkMetric("cpu_usage", float64(i)*1.5,
			base.Add(time.Duration(i)*15*time.Second),
			map[string]string{"host": "a"}))
	}
	require.NoError(t, s.Write(ctx, in))

	got, err := s.Query(ctx, storage.QueryRequest{
		Start:       base.Add(-time.Hour),
		End:         base.Add(time.Hour),
		MetricNames: []string{"cpu_usage"},
	})
	require.NoError(t, err)
	require.Len(t, got, 50)

	for i, m := range got {
		require.Equal(t, "cpu_usage", m.Name)
		require.Equal(t, float64(i)*1.5, m.Value)
		require.Equal(t, "a", m.Labels["host"])
		require.Equal(t, metrics.MetricType("gauge"), m.Type)
		// Millisecond truncation is the documented tradeoff (ADR 0002).
		require.Equal(t, in[i].Timestamp.UnixMilli(), m.Timestamp.UnixMilli())
	}
}

func TestInternalLabelsAreNotLeaked(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, s.Write(ctx, []metrics.Metric{
		mkMetric("m", 1, now, map[string]string{"host": "a"}),
	}))

	got, err := s.Query(ctx, storage.QueryRequest{Start: now.Add(-time.Hour), End: now.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, got, 1)

	require.NotContains(t, got[0].Labels, nameLabel, "__name__ is storage identity, not user data")
	require.NotContains(t, got[0].Labels, typeLabel, "__type__ is storage identity, not user data")
	require.Equal(t, map[string]string{"host": "a"}, got[0].Labels)
}

func TestSpansMultipleChunks(t *testing.T) {
	// More samples than fit in one chunk, so this exercises cutting and the
	// merge of flushed chunks with the open head.
	s, _ := newTestStore(t)
	ctx := context.Background()

	base := time.Now().Truncate(time.Millisecond)
	n := 300
	var in []metrics.Metric
	for i := 0; i < n; i++ {
		in = append(in, mkMetric("requests", float64(i),
			base.Add(time.Duration(i)*time.Second), nil))
	}
	require.NoError(t, s.Write(ctx, in))

	got, err := s.Query(ctx, storage.QueryRequest{
		Start: base.Add(-time.Hour), End: base.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, got, n)
	for i, m := range got {
		require.Equal(t, float64(i), m.Value, "sample %d", i)
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	// The series index lives in memory but chunk keys reference series IDs, so
	// a reopen that failed to rebuild it would orphan every chunk on disk.
	dir := t.TempDir()
	base := time.Now().Truncate(time.Millisecond)

	s, err := New(Config{Path: dir, MaxMemoryMB: 64})
	require.NoError(t, err)

	var in []metrics.Metric
	for i := 0; i < 40; i++ {
		in = append(in, mkMetric("persisted", float64(i),
			base.Add(time.Duration(i)*time.Second),
			map[string]string{"shard": fmt.Sprintf("%d", i%3)}))
	}
	require.NoError(t, s.Write(context.Background(), in))
	require.NoError(t, s.Close())

	reopened, err := New(Config{Path: dir, MaxMemoryMB: 64})
	require.NoError(t, err)
	defer reopened.Close()

	require.Equal(t, 3, reopened.index.Len(), "index must be rebuilt from disk")

	got, err := reopened.Query(context.Background(), storage.QueryRequest{
		Start: base.Add(-time.Hour), End: base.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, got, 40)
}

func TestOutOfOrderOpensNewChunk(t *testing.T) {
	// The encoder needs non-decreasing timestamps. A backwards sample must open
	// a new chunk rather than be dropped or corrupt the existing one.
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Millisecond)

	require.NoError(t, s.Write(ctx, []metrics.Metric{
		mkMetric("ooo", 1, base.Add(10*time.Second), nil),
		mkMetric("ooo", 2, base.Add(20*time.Second), nil),
		mkMetric("ooo", 3, base.Add(5*time.Second), nil), // backwards
		mkMetric("ooo", 4, base.Add(30*time.Second), nil),
	}))

	got, err := s.Query(ctx, storage.QueryRequest{
		Start: base.Add(-time.Hour), End: base.Add(time.Hour),
	})
	require.NoError(t, err)
	require.Len(t, got, 4, "no sample may be lost")

	values := make([]float64, len(got))
	for i, m := range got {
		values[i] = m.Value
	}
	// Results come back time-ordered, so the late sample sorts into place.
	require.Equal(t, []float64{3, 1, 2, 4}, values)
}

func TestLabelFiltering(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, s.Write(ctx, []metrics.Metric{
		mkMetric("m", 1, now, map[string]string{"env": "prod", "host": "a"}),
		mkMetric("m", 2, now, map[string]string{"env": "dev", "host": "b"}),
		mkMetric("other", 3, now, map[string]string{"env": "prod"}),
	}))

	got, err := s.Query(ctx, storage.QueryRequest{
		Start: now.Add(-time.Hour), End: now.Add(time.Hour),
		MetricNames: []string{"m"},
		Labels:      map[string]string{"env": "prod"},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 1.0, got[0].Value)
}

func TestQueryTimeRangeAndLimit(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Millisecond)

	var in []metrics.Metric
	for i := 0; i < 100; i++ {
		in = append(in, mkMetric("m", float64(i), base.Add(time.Duration(i)*time.Second), nil))
	}
	require.NoError(t, s.Write(ctx, in))

	got, err := s.Query(ctx, storage.QueryRequest{
		Start: base.Add(10 * time.Second),
		End:   base.Add(19 * time.Second),
	})
	require.NoError(t, err)
	require.Len(t, got, 10)
	require.Equal(t, 10.0, got[0].Value)

	limited, err := s.Query(ctx, storage.QueryRequest{
		Start: base.Add(-time.Hour), End: base.Add(time.Hour), Limit: 5,
	})
	require.NoError(t, err)
	require.Len(t, limited, 5)
}

func TestDeleteIsChunkGranular(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Millisecond).Add(-24 * time.Hour)

	var in []metrics.Metric
	for i := 0; i < 400; i++ {
		in = append(in, mkMetric("m", float64(i), base.Add(time.Duration(i)*time.Minute), nil))
	}
	require.NoError(t, s.Write(ctx, in))
	require.NoError(t, s.Flush())

	cutoff := base.Add(200 * time.Minute)
	require.NoError(t, s.Delete(ctx, storage.DeleteOptions{Before: cutoff}))

	got, err := s.Query(ctx, storage.QueryRequest{
		Start: base.Add(-time.Hour), End: base.Add(48 * time.Hour),
	})
	require.NoError(t, err)

	// Everything at or after the cutoff must survive. Some data before it may
	// survive too, bounded by one chunk per series, which is documented.
	var survivedAfter int
	for _, m := range got {
		if !m.Timestamp.Before(cutoff) {
			survivedAfter++
		}
	}
	require.Equal(t, 200, survivedAfter, "no data at or after the cutoff may be deleted")
	require.Less(t, len(got), 400, "data before the cutoff should have been removed")
}

func TestStats(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Millisecond)

	var in []metrics.Metric
	for i := 0; i < 250; i++ {
		in = append(in, mkMetric("m", float64(i),
			base.Add(time.Duration(i)*time.Second),
			map[string]string{"host": fmt.Sprintf("h%d", i%5)}))
	}
	require.NoError(t, s.Write(ctx, in))

	st, err := s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), st.TotalSeries)
	require.Equal(t, uint64(250), st.TotalMetrics, "head chunks must be counted")
}

func TestEmptyQuery(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.Query(context.Background(), storage.QueryRequest{
		Start: time.Now().Add(-time.Hour), End: time.Now(),
	})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestContextCancellation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Write(ctx, []metrics.Metric{mkMetric("m", 1, time.Now(), nil)})
	require.ErrorIs(t, err, context.Canceled)

	_, err = s.Query(ctx, storage.QueryRequest{Start: time.Now(), End: time.Now()})
	require.ErrorIs(t, err, context.Canceled)
}

func dirSize(p string) int64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// TestStorageComparison runs the same workload through both backends and
// reports bytes per sample on disk.
//
// This is the test that justifies the package existing. The badger backend
// stores one JSON document per sample including a full copy of the label set;
// this one stores compressed chunks against an interned series index.
//
// The sample count is deliberately large. BadgerDB has a fixed on-disk
// footprint of roughly a megabyte before it holds anything useful (SST headers,
// bloom filters, the manifest, value-log preallocation), and at small volumes
// that overhead is the measurement rather than the encoding. Measured on the
// chunked backend alone:
//
//	60,000 samples    1.04 MB    18.09 bytes/sample
//	240,000 samples   1.19 MB     5.20 bytes/sample
//	960,000 samples   2.00 MB     2.18 bytes/sample
//
// Only the last figure says anything about the codec. Benchmarking this at
// 60k samples would have reported a 3.4x improvement and been honest about
// nothing.
func TestStorageComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("writes ~1M samples through two backends")
	}

	const seriesCount = 200
	const perSeries = 2400
	total := seriesCount * perSeries

	base := time.Now().Truncate(time.Millisecond).Add(-time.Duration(perSeries) * 15 * time.Second)

	build := func(i, j int) metrics.Metric {
		return metrics.Metric{
			Name:  "http_requests_total",
			Type:  metrics.MetricType("counter"),
			Value: float64(j) * 1.5,
			Labels: map[string]string{
				"service":  fmt.Sprintf("api-%d", i%20),
				"method":   []string{"GET", "POST", "PUT"}[i%3],
				"status":   []string{"200", "404", "500"}[i%3],
				"instance": fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			},
			Timestamp: base.Add(time.Duration(j) * 15 * time.Second),
		}
	}

	ctx := context.Background()

	// Old engine.
	oldDir := t.TempDir()
	oldStore, err := badgerstore.New(badgerstore.Config{Path: oldDir, MaxMemoryMB: 256})
	require.NoError(t, err)

	oldStart := time.Now()
	batch := make([]metrics.Metric, 0, 1000)
	for i := 0; i < seriesCount; i++ {
		for j := 0; j < perSeries; j++ {
			batch = append(batch, build(i, j))
			if len(batch) == 1000 {
				require.NoError(t, oldStore.Write(ctx, batch))
				batch = batch[:0]
			}
		}
	}
	if len(batch) > 0 {
		require.NoError(t, oldStore.Write(ctx, batch))
	}
	oldElapsed := time.Since(oldStart)
	require.NoError(t, oldStore.Close())
	oldSize := dirSize(oldDir)

	// New engine.
	newDir := t.TempDir()
	newStore, err := New(Config{Path: newDir, MaxMemoryMB: 256})
	require.NoError(t, err)

	newStart := time.Now()
	batch = batch[:0]
	for i := 0; i < seriesCount; i++ {
		for j := 0; j < perSeries; j++ {
			batch = append(batch, build(i, j))
			if len(batch) == 1000 {
				require.NoError(t, newStore.Write(ctx, batch))
				batch = batch[:0]
			}
		}
	}
	if len(batch) > 0 {
		require.NoError(t, newStore.Write(ctx, batch))
	}
	require.NoError(t, newStore.Flush())
	newElapsed := time.Since(newStart)

	st, err := newStore.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(seriesCount), st.TotalSeries)
	require.Equal(t, uint64(total), st.TotalMetrics)

	require.NoError(t, newStore.Close())
	newSize := dirSize(newDir)

	oldBPS := float64(oldSize) / float64(total)
	newBPS := float64(newSize) / float64(total)

	fmt.Printf("\n===== STORAGE COMPARISON =====\n")
	fmt.Printf("series           : %d\n", seriesCount)
	fmt.Printf("samples          : %d\n", total)
	fmt.Printf("\n%-12s %14s %14s %12s\n", "backend", "on-disk", "bytes/sample", "ingest/sec")
	fmt.Printf("%s\n", "------------------------------------------------------------")
	fmt.Printf("%-12s %11.1f MB %14.1f %12.0f\n", "badger",
		float64(oldSize)/(1024*1024), oldBPS, float64(total)/oldElapsed.Seconds())
	fmt.Printf("%-12s %11.1f MB %14.1f %12.0f\n", "chunked",
		float64(newSize)/(1024*1024), newBPS, float64(total)/newElapsed.Seconds())
	fmt.Printf("\nimprovement      : %.1fx smaller\n", oldBPS/newBPS)
	fmt.Printf("note             : both figures include BadgerDB's ~1 MB fixed\n")
	fmt.Printf("                   footprint, which is why this runs at %dk samples\n", total/1000)
	fmt.Printf("==============================\n\n")

	require.Less(t, newBPS, oldBPS/8,
		"chunked storage should be at least 8x smaller; got %.1f vs %.1f bytes/sample", newBPS, oldBPS)
}
