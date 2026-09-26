//go:build storagebench

// Storage design comparison. Run with:
//
//	go test -tags storagebench -run TestStorageComparison -v -timeout 30m ./pkg/tsdb/
//
// It ingests the same workload into four storage designs and reports disk
// usage, ingest throughput, startup time, heap after startup and query latency.
// The results decide whether compressed chunks earn their complexity.
package tsdb

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb/chunk"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
	oldbadger "github.com/nicktill/tinyobs/pkg/storage/badger"
)

// Workload: 20 metrics x 100 series, scraped every 15s for 6 hours.
const (
	cmpMetrics         = 20
	cmpSeriesPerMetric = 100
	cmpInterval        = 15000
	cmpScrapes         = 6 * 240
)

type cmpStore interface {
	append(batch []cmpSample) error
	// query returns the number of samples for series matching name (and
	// instance, if non-empty) in [mint, maxt].
	query(name, instance string, mint, maxt int64) (int, error)
	close() error
	// compact forces a full LSM compaction so disk size reflects steady state.
	compact() error
}

type cmpSample struct {
	lset labels.Labels
	t    int64
	v    float64
}

type cmpDesign struct {
	name string
	open func(dir string) (cmpStore, error)
}

func TestStorageComparison(t *testing.T) {
	designs := []cmpDesign{
		{"A current: KV per sample, JSON", openOld},
		{"B KV per sample, binary", openKV},
		{"B-zstd KV per sample, binary", func(dir string) (cmpStore, error) { return openKVWith(dir, true) }},
		{"C chunks, uncompressed", func(dir string) (cmpStore, error) { return openChunks(dir, false) }},
		{"D chunks, Gorilla (tsdb)", func(dir string) (cmpStore, error) { return openChunks(dir, true) }},
		{"E WAL + write-once Gorilla chunks", openWALChunks},
	}
	batches, start := cmpWorkload()
	total := len(batches) * len(batches[0])
	t.Logf("workload: %d series, %d scrapes, %d samples", len(batches[0]), len(batches), total)

	for _, d := range designs {
		dir := t.TempDir()
		s, err := d.open(dir)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		for _, b := range batches {
			if err := s.append(b); err != nil {
				t.Fatal(err)
			}
		}
		ingest := time.Since(t0)
		if err := s.close(); err != nil {
			t.Fatal(err)
		}
		disk := dirSize(dir)

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		t0 = time.Now()
		s, err = d.open(dir)
		if err != nil {
			t.Fatal(err)
		}
		startup := time.Since(t0)
		if err := s.compact(); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		heap := int64(after.HeapInuse) - int64(before.HeapInuse)

		end := start + int64(cmpScrapes-1)*cmpInterval + 10 // covers scrape jitter
		// Q1: 10 single series over the full 6h (a detail panel).
		t0 = time.Now()
		for i := 0; i < 10; i++ {
			n, err := s.query(fmt.Sprintf("metric_%d", i), fmt.Sprintf("host-%d", i), start, end)
			if err != nil || n != cmpScrapes {
				t.Fatalf("%s Q1: n=%d err=%v", d.name, n, err)
			}
		}
		q1 := time.Since(t0) / 10
		// Q2: one metric, all 100 series, last hour (a sum by (...) panel).
		t0 = time.Now()
		n, err := s.query("metric_3", "", end-10-3600_000, end)
		if err != nil || n != cmpSeriesPerMetric*241 {
			t.Fatalf("%s Q2: n=%d err=%v", d.name, n, err)
		}
		q2 := time.Since(t0)
		s.close()
		compacted := dirSize(dir)

		t.Logf("%-34s disk %6.2f B/sample (compacted %6.2f) | ingest %8.0f samples/s | startup %7v | heap %6.1f MB | Q1 %7v | Q2 %7v",
			d.name, float64(disk)/float64(total), float64(compacted)/float64(total), float64(total)/ingest.Seconds(),
			startup.Round(time.Millisecond), float64(heap)/(1<<20), q1.Round(time.Microsecond), q2.Round(time.Microsecond))
	}
}

// cmpWorkload generates realistic values: half counters, a third gauges with
// two decimals, the rest full-precision noisy floats (Gorilla's worst case).
func cmpWorkload() ([][]cmpSample, int64) {
	rng := rand.New(rand.NewSource(42))
	start := time.Now().Add(-6 * time.Hour).UnixMilli()
	var sets []labels.Labels
	for m := 0; m < cmpMetrics; m++ {
		for i := 0; i < cmpSeriesPerMetric; i++ {
			sets = append(sets, labels.FromStrings("__name__", fmt.Sprintf("metric_%d", m),
				"instance", fmt.Sprintf("host-%d", i), "job", "api", "path", fmt.Sprintf("/v1/r%d", i%10)))
		}
	}
	vals := make([]float64, len(sets))
	batches := make([][]cmpSample, cmpScrapes)
	for sc := range batches {
		b := make([]cmpSample, len(sets))
		for i, ls := range sets {
			switch m := i / cmpSeriesPerMetric; {
			case m%10 < 5:
				vals[i] += float64(rng.Intn(20))
			case m%10 < 8:
				vals[i] = math.Round((50+rng.NormFloat64()*5)*100) / 100
			default:
				vals[i] = rng.Float64() * 1e3
			}
			jitter := int64(rng.Intn(5))
			b[i] = cmpSample{ls, start + int64(sc)*cmpInterval + jitter, vals[i]}
		}
		batches[sc] = b
	}
	return batches, start
}

func dirSize(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// A: the storage TinyObs ships today.

type oldStore struct{ s *oldbadger.Storage }

func openOld(dir string) (cmpStore, error) {
	s, err := oldbadger.New(oldbadger.Config{Path: dir})
	return &oldStore{s}, err
}

func (o *oldStore) append(batch []cmpSample) error {
	ms := make([]metrics.Metric, len(batch))
	for i, s := range batch {
		lm := s.lset.DropMetricName().Map()
		ms[i] = metrics.Metric{Name: s.lset.Get("__name__"), Type: metrics.GaugeType, Value: s.v, Labels: lm, Timestamp: time.UnixMilli(s.t)}
	}
	return o.s.Write(context.Background(), ms)
}

func (o *oldStore) query(name, instance string, mint, maxt int64) (int, error) {
	req := storage.QueryRequest{Start: time.UnixMilli(mint), End: time.UnixMilli(maxt + 1), MetricNames: []string{name}}
	if instance != "" {
		req.Labels = map[string]string{"instance": instance}
	}
	out, err := o.s.Query(context.Background(), req)
	return len(out), err
}

func (o *oldStore) close() error { return o.s.Close() }

// B: one key per sample, binary. Series ids and an in-memory index as in tsdb.

type kvStore struct {
	kv     *badger.DB
	ids    map[uint64]uint64 // label hash → id
	byName map[string][]kvRef
	next   uint64
}

type kvRef struct {
	id   uint64
	lset labels.Labels
}

func openKV(dir string) (cmpStore, error) { return openKVWith(dir, false) }

func openKVWith(dir string, zstd bool) (cmpStore, error) {
	o := badger.DefaultOptions(dir).WithLogger(nil).WithMemTableSize(16 << 20).WithNumMemtables(2).WithBlockCacheSize(16 << 20).WithIndexCacheSize(8 << 20)
	if zstd {
		o = o.WithCompression(options.ZSTD).WithZSTDCompressionLevel(3)
	}
	kv, err := badger.Open(o)
	if err != nil {
		return nil, err
	}
	s := &kvStore{kv: kv, ids: map[uint64]uint64{}, byName: map[string][]kvRef{}, next: 1}
	err = kv.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte{'s'}
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			id := binary.BigEndian.Uint64(it.Item().Key()[1:])
			b, _ := it.Item().ValueCopy(nil)
			ls, err := decodeLabels(b)
			if err != nil {
				return err
			}
			s.index(id, ls)
			if id >= s.next {
				s.next = id + 1
			}
		}
		return nil
	})
	return s, err
}

func (s *kvStore) index(id uint64, ls labels.Labels) {
	s.ids[ls.Hash()] = id
	n := ls.Get("__name__")
	s.byName[n] = append(s.byName[n], kvRef{id, ls})
}

func (s *kvStore) append(batch []cmpSample) error {
	wb := s.kv.NewWriteBatch()
	defer wb.Cancel()
	for _, smp := range batch {
		id, ok := s.ids[smp.lset.Hash()]
		if !ok {
			id = s.next
			s.next++
			s.index(id, smp.lset)
			if err := wb.Set(seriesKey(id), encodeLabels(smp.lset)); err != nil {
				return err
			}
		}
		k := make([]byte, 17)
		k[0] = 'd'
		binary.BigEndian.PutUint64(k[1:], id)
		binary.BigEndian.PutUint64(k[9:], uint64(smp.t)^(1<<63))
		v := make([]byte, 8)
		binary.BigEndian.PutUint64(v, math.Float64bits(smp.v))
		if err := wb.Set(k, v); err != nil {
			return err
		}
	}
	return wb.Flush()
}

func (s *kvStore) query(name, instance string, mint, maxt int64) (int, error) {
	n := 0
	err := s.kv.View(func(txn *badger.Txn) error {
		for _, r := range s.byName[name] {
			if instance != "" && r.lset.Get("instance") != instance {
				continue
			}
			prefix := make([]byte, 9)
			prefix[0] = 'd'
			binary.BigEndian.PutUint64(prefix[1:], r.id)
			seek := append(append([]byte{}, prefix...), make([]byte, 8)...)
			binary.BigEndian.PutUint64(seek[9:], uint64(mint)^(1<<63))
			opts := badger.DefaultIteratorOptions
			opts.Prefix = prefix
			it := txn.NewIterator(opts)
			for it.Seek(seek); it.Valid(); it.Next() {
				t := int64(binary.BigEndian.Uint64(it.Item().Key()[9:]) ^ (1 << 63))
				if t > maxt {
					break
				}
				if err := it.Item().Value(func(v []byte) error { _ = math.Float64frombits(binary.BigEndian.Uint64(v)); return nil }); err != nil {
					it.Close()
					return err
				}
				n++
			}
			it.Close()
		}
		return nil
	})
	return n, err
}

func (s *kvStore) close() error { return s.kv.Close() }

// C and D: the tsdb package, optionally with compression disabled.

type chunkStore struct {
	db  *DB
	raw *rawChunks
}

func openChunks(dir string, compressed bool) (cmpStore, error) {
	if compressed {
		db, err := Open(Options{Dir: dir})
		return &chunkStore{db: db}, err
	}
	r, err := openRawChunks(dir)
	return &chunkStore{raw: r}, err
}

func (c *chunkStore) append(batch []cmpSample) error {
	if c.raw != nil {
		return c.raw.append(batch)
	}
	app := c.db.Appender()
	for _, s := range batch {
		app.Append(s.lset, s.t, s.v)
	}
	res, err := app.Commit()
	if err == nil && res.Rejected() > 0 {
		err = res.Err()
	}
	return err
}

func (c *chunkStore) query(name, instance string, mint, maxt int64) (int, error) {
	if c.raw != nil {
		return c.raw.query(name, instance, mint, maxt)
	}
	ms := []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, "__name__", name)}
	if instance != "" {
		ms = append(ms, labels.MustNewMatcher(labels.MatchEqual, "instance", instance))
	}
	out, err := c.db.Select(context.Background(), mint, maxt, 0, ms...)
	n := 0
	for _, s := range out {
		n += len(s.Samples)
	}
	return n, err
}

func (w *walChunks) close() error { return w.kv.Close() }

func (c *chunkStore) close() error {
	if c.raw != nil {
		return c.raw.kv.Close()
	}
	return c.db.Close()
}

// rawChunks is design C: the same chunk layout as tsdb, but samples are stored
// as fixed 16-byte (timestamp, value) pairs.
type rawChunks struct {
	*kvStore
	head map[uint64]*rawHead
}

type rawHead struct {
	minT int64
	buf  []byte
}

func openRawChunks(dir string) (*rawChunks, error) {
	s, err := openKV(dir)
	if err != nil {
		return nil, err
	}
	r := &rawChunks{kvStore: s.(*kvStore), head: map[uint64]*rawHead{}}
	// Reload each series' last chunk as its head, as tsdb does.
	err = r.kv.View(func(txn *badger.Txn) error {
		for _, refs := range r.byName {
			for _, ref := range refs {
				opts := badger.DefaultIteratorOptions
				opts.Prefix = chunkPrefix(ref.id)
				opts.Reverse = true
				it := txn.NewIterator(opts)
				it.Seek(append(chunkPrefix(ref.id), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff))
				if it.Valid() {
					b, _ := it.Item().ValueCopy(nil)
					r.head[ref.id] = &rawHead{chunkKeyMinT(it.Item().Key()), b}
				}
				it.Close()
			}
		}
		return nil
	})
	return r, err
}

func (r *rawChunks) append(batch []cmpSample) error {
	wb := r.kv.NewWriteBatch()
	defer wb.Cancel()
	for _, smp := range batch {
		id, ok := r.ids[smp.lset.Hash()]
		if !ok {
			id = r.next
			r.next++
			r.index(id, smp.lset)
			if err := wb.Set(seriesKey(id), encodeLabels(smp.lset)); err != nil {
				return err
			}
		}
		h := r.head[id]
		if h == nil || len(h.buf)/16 >= maxChunkSamples || smp.t-h.minT >= maxChunkSpan {
			h = &rawHead{minT: smp.t}
			r.head[id] = h
		}
		h.buf = binary.BigEndian.AppendUint64(h.buf, uint64(smp.t))
		h.buf = binary.BigEndian.AppendUint64(h.buf, math.Float64bits(smp.v))
		if err := wb.Set(chunkKey(id, h.minT), clone(h.buf)); err != nil {
			return err
		}
	}
	return wb.Flush()
}

func (r *rawChunks) query(name, instance string, mint, maxt int64) (int, error) {
	n := 0
	refs := r.byName[name]
	sort.Slice(refs, func(i, j int) bool { return refs[i].id < refs[j].id })
	err := r.kv.View(func(txn *badger.Txn) error {
		for _, ref := range refs {
			if instance != "" && ref.lset.Get("instance") != instance {
				continue
			}
			opts := badger.DefaultIteratorOptions
			opts.Prefix = chunkPrefix(ref.id)
			it := txn.NewIterator(opts)
			for it.Seek(chunkKey(ref.id, mint-maxChunkSpan+1)); it.Valid(); it.Next() {
				if chunkKeyMinT(it.Item().Key()) > maxt {
					break
				}
				it.Item().Value(func(b []byte) error {
					for i := 0; i+16 <= len(b); i += 16 {
						t := int64(binary.BigEndian.Uint64(b[i:]))
						if t >= mint && t <= maxt {
							n++
						}
					}
					return nil
				})
			}
			it.Close()
		}
		return nil
	})
	return n, err
}

// TestEncodingOnly measures the chunk encodings alone, without Badger, to
// separate the cost of the format from the cost of how it is persisted.
func TestEncodingOnly(t *testing.T) {
	batches, _ := cmpWorkload()
	n := len(batches[0])
	gorilla, raw := 0, 0
	for s := 0; s < n; s++ {
		for c := 0; c < len(batches); c += maxChunkSamples {
			ch := chunk.New()
			app, _ := ch.Appender()
			cnt := 0
			for sc := c; sc < c+maxChunkSamples && sc < len(batches); sc++ {
				app.Append(batches[sc][s].t, batches[sc][s].v)
				cnt++
			}
			gorilla += len(ch.Bytes())
			raw += cnt * 16
		}
	}
	total := n * len(batches)
	t.Logf("gorilla %.2f B/sample, raw %.2f B/sample", float64(gorilla)/float64(total), float64(raw)/float64(total))
}

// E: samples go to a write-ahead log (one small key each, as in B) and to an
// in-memory head chunk. A finished chunk is written once and its WAL entries
// are deleted. Startup replays the WAL into head chunks.
type walChunks struct {
	*kvStore
	head map[uint64]*walHead
}

type walHead struct {
	minT int64
	c    *chunk.Chunk
	app  *chunk.Appender
	ts   []int64
}

func walKey(id uint64, t int64) []byte {
	k := make([]byte, 17)
	k[0] = 'w'
	binary.BigEndian.PutUint64(k[1:], id)
	binary.BigEndian.PutUint64(k[9:], uint64(t)^(1<<63))
	return k
}

func openWALChunks(dir string) (cmpStore, error) {
	s, err := openKV(dir)
	if err != nil {
		return nil, err
	}
	w := &walChunks{kvStore: s.(*kvStore), head: map[uint64]*walHead{}}
	err = w.kv.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte{'w'}
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			k := it.Item().Key()
			id := binary.BigEndian.Uint64(k[1:])
			t := int64(binary.BigEndian.Uint64(k[9:]) ^ (1 << 63))
			var v float64
			it.Item().Value(func(b []byte) error { v = math.Float64frombits(binary.BigEndian.Uint64(b)); return nil })
			h := w.head[id]
			if h == nil {
				h = &walHead{minT: t, c: chunk.New()}
				h.app, _ = h.c.Appender()
				w.head[id] = h
			}
			h.app.Append(t, v)
			h.ts = append(h.ts, t)
		}
		return nil
	})
	return w, err
}

func (w *walChunks) append(batch []cmpSample) error {
	wb := w.kv.NewWriteBatch()
	defer wb.Cancel()
	for _, smp := range batch {
		id, ok := w.ids[smp.lset.Hash()]
		if !ok {
			id = w.next
			w.next++
			w.index(id, smp.lset)
			if err := wb.Set(seriesKey(id), encodeLabels(smp.lset)); err != nil {
				return err
			}
		}
		h := w.head[id]
		if h != nil && (len(h.ts) >= maxChunkSamples || smp.t-h.minT >= maxChunkSpan) {
			if err := wb.Set(chunkKey(id, h.minT), clone(h.c.Bytes())); err != nil {
				return err
			}
			for _, t := range h.ts {
				if err := wb.Delete(walKey(id, t)); err != nil {
					return err
				}
			}
			h = nil
		}
		if h == nil {
			h = &walHead{minT: smp.t, c: chunk.New()}
			h.app, _ = h.c.Appender()
			w.head[id] = h
		}
		h.app.Append(smp.t, smp.v)
		h.ts = append(h.ts, smp.t)
		v := make([]byte, 8)
		binary.BigEndian.PutUint64(v, math.Float64bits(smp.v))
		if err := wb.Set(walKey(id, smp.t), v); err != nil {
			return err
		}
	}
	return wb.Flush()
}

func (w *walChunks) query(name, instance string, mint, maxt int64) (int, error) {
	n := 0
	err := w.kv.View(func(txn *badger.Txn) error {
		for _, ref := range w.byName[name] {
			if instance != "" && ref.lset.Get("instance") != instance {
				continue
			}
			opts := badger.DefaultIteratorOptions
			opts.Prefix = chunkPrefix(ref.id)
			it := txn.NewIterator(opts)
			for it.Seek(chunkKey(ref.id, mint-maxChunkSpan+1)); it.Valid(); it.Next() {
				if chunkKeyMinT(it.Item().Key()) > maxt {
					break
				}
				it.Item().Value(func(b []byte) error {
					c, _ := chunk.FromBytes(b)
					ci := c.Iterator()
					for ci.Next() {
						if t, _ := ci.At(); t >= mint && t <= maxt {
							n++
						}
					}
					return nil
				})
			}
			it.Close()
			if h := w.head[ref.id]; h != nil {
				ci := h.c.Iterator()
				for ci.Next() {
					if t, _ := ci.At(); t >= mint && t <= maxt {
						n++
					}
				}
			}
		}
		return nil
	})
	return n, err
}

func flatten(kv *badger.DB) error {
	if err := kv.Flatten(4); err != nil {
		return err
	}
	for kv.RunValueLogGC(0.1) == nil {
	}
	return nil
}

func (o *oldStore) compact() error   { return nil } // no access to its Badger handle
func (s *kvStore) compact() error    { return flatten(s.kv) }
func (c *chunkStore) compact() error {
	if c.raw != nil {
		return flatten(c.raw.kv)
	}
	return flatten(c.db.kv)
}
