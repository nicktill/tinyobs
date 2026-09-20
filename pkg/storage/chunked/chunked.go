// Package chunked implements storage.Storage on top of chunk-compressed
// samples and an interned series index.
//
// It exists to make the compression in pkg/chunkenc and the label interning in
// pkg/series actually reachable from the running server. The badger backend
// writes one JSON document per sample and measures 55.6 bytes/sample on disk;
// this one writes one compressed chunk per (series, window) and measures in the
// low single digits. TestStorageComparison runs both against the same workload.
package chunked

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/nicktill/tinyobs/pkg/chunkenc"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/series"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// Key prefixes. Chunks and series entries share a keyspace, so the first byte
// keeps their iteration ranges disjoint.
const (
	prefixSeries byte = 0x01
	prefixChunk  byte = 0x02
)

// nameLabel carries the metric name inside the label set, so a series is
// identified by exactly one thing rather than by a name plus a label set that
// have to be kept in sync.
const nameLabel = "__name__"

// typeLabel records the metric type alongside the series rather than on every
// sample, which is where the old engine put it.
const typeLabel = "__type__"

// Config configures the backend.
type Config struct {
	// Path to the database directory.
	Path string
	// InMemory runs without touching disk, for tests.
	InMemory bool
	// MaxMemoryMB caps BadgerDB memory. 0 selects a laptop-friendly default.
	MaxMemoryMB int64
}

// Storage is a chunk-compressed metric store.
type Storage struct {
	db    *badger.DB
	index *series.Index

	mu sync.Mutex
	// head holds the open, still-appendable chunk for each series. A chunk is
	// flushed when it fills or when the store closes.
	//
	// Known gap: head chunks live only in memory, so an unclean shutdown loses
	// up to MaxSamplesPerChunk samples per series. A real TSDB writes a WAL
	// ahead of the head for exactly this reason. That is the next piece of work
	// and is deliberately not faked here.
	head map[series.ID]*headChunk
}

type headChunk struct {
	chunk   *chunkenc.XORChunk
	app     *chunkenc.Appender
	startMs int64
	lastMs  int64
}

// New opens a chunked store.
func New(cfg Config) (*Storage, error) {
	opts := badger.DefaultOptions(cfg.Path)
	if cfg.InMemory {
		opts = opts.WithInMemory(true).WithDir("").WithValueDir("")
	}
	memMB := cfg.MaxMemoryMB
	if memMB <= 0 {
		memMB = 48
	}
	opts = opts.
		WithMemTableSize(memMB * 1024 * 1024 / 3).
		WithNumMemtables(2).
		WithNumLevelZeroTables(2).
		WithNumLevelZeroTablesStall(4).
		WithLogger(nil)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("chunked: open badger: %w", err)
	}

	s := &Storage{
		db:    db,
		index: series.NewIndex(),
		head:  make(map[series.ID]*headChunk),
	}
	if err := s.loadIndex(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// loadIndex rebuilds the in-memory series index from disk at startup. Chunk
// keys reference series IDs, so without this every stored chunk is orphaned.
func (s *Storage) loadIndex() error {
	var entries []series.Entry

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte{prefixSeries}
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.ValidForPrefix([]byte{prefixSeries}); it.Next() {
			item := it.Item()
			key := item.Key()
			if len(key) != 9 {
				continue
			}
			id := series.ID(binary.BigEndian.Uint64(key[1:]))

			err := item.Value(func(val []byte) error {
				ls, err := series.Decode(val)
				if err != nil {
					return fmt.Errorf("chunked: decode series %d: %w", id, err)
				}
				entries = append(entries, series.Entry{ID: id, Labels: ls})
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.index.Load(entries)
}

func seriesKey(id series.ID) []byte {
	k := make([]byte, 9)
	k[0] = prefixSeries
	binary.BigEndian.PutUint64(k[1:], uint64(id))
	return k
}

func chunkKey(id series.ID, startMs int64) []byte {
	k := make([]byte, 17)
	k[0] = prefixChunk
	binary.BigEndian.PutUint64(k[1:9], uint64(id))
	// Offset by the int64 minimum so the unsigned big-endian ordering matches
	// signed timestamp ordering, keeping range scans correct for pre-epoch data.
	binary.BigEndian.PutUint64(k[9:17], uint64(startMs)^(1<<63))
	return k
}

func chunkKeyTime(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[9:17]) ^ (1 << 63))
}

// labelsFor builds the full identifying label set for a metric.
func labelsFor(m metrics.Metric) series.Labels {
	ls := make(series.Labels, 0, len(m.Labels)+2)
	ls = append(ls, series.Label{Name: nameLabel, Value: m.Name})
	if m.Type != "" {
		ls = append(ls, series.Label{Name: typeLabel, Value: string(m.Type)})
	}
	for k, v := range m.Labels {
		ls = append(ls, series.Label{Name: k, Value: v})
	}
	ls.Sort()
	return ls
}

// Write appends samples. Timestamps are truncated to milliseconds; see
// docs/adr/0002-millisecond-timestamps.md.
func (s *Storage) Write(ctx context.Context, ms []metrics.Metric) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var newSeries []series.Entry
	var full []flushable

	for i, m := range ms {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}

		ls := labelsFor(m)
		id, created := s.index.GetOrCreate(ls)
		if created {
			newSeries = append(newSeries, series.Entry{ID: id, Labels: ls})
		}

		tsMs := m.Timestamp.UnixMilli()
		h := s.head[id]

		// A new chunk is cut when there is no head, the head is full, or the
		// sample would go backwards. The encoder requires non-decreasing
		// timestamps within a chunk, so out-of-order arrivals open a new one
		// rather than being dropped or forcing the codec to cope.
		if h == nil || h.chunk.NumSamples() >= chunkenc.MaxSamplesPerChunk || tsMs < h.lastMs {
			if h != nil {
				full = append(full, flushable{id: id, h: h})
			}
			c := chunkenc.NewXORChunk()
			app, err := c.Appender()
			if err != nil {
				return fmt.Errorf("chunked: new appender: %w", err)
			}
			h = &headChunk{chunk: c, app: app, startMs: tsMs}
			s.head[id] = h
		}

		if err := h.app.Append(tsMs, m.Value); err != nil {
			return fmt.Errorf("chunked: append sample: %w", err)
		}
		h.lastMs = tsMs
	}

	if len(newSeries) == 0 && len(full) == 0 {
		return nil
	}
	return s.persist(newSeries, full)
}

type flushable struct {
	id series.ID
	h  *headChunk
}

// persist writes new series entries and filled chunks in one batch.
//
// WriteBatch rather than a single transaction: a Badger txn has a size ceiling,
// and a large ingest batch that exceeds it fails wholesale. WriteBatch commits
// in chunks and is the documented path for bulk writes.
func (s *Storage) persist(newSeries []series.Entry, full []flushable) error {
	wb := s.db.NewWriteBatch()
	defer wb.Cancel()

	for _, e := range newSeries {
		if err := wb.Set(seriesKey(e.ID), e.Labels.Encode()); err != nil {
			return fmt.Errorf("chunked: write series entry: %w", err)
		}
	}
	for _, f := range full {
		if err := wb.Set(chunkKey(f.id, f.h.startMs), f.h.chunk.Bytes()); err != nil {
			return fmt.Errorf("chunked: write chunk: %w", err)
		}
	}
	return wb.Flush()
}

// Flush persists every open head chunk. Called by Close, and available to
// callers that want a durability point without shutting down.
func (s *Storage) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *Storage) flushLocked() error {
	if len(s.head) == 0 {
		return nil
	}
	full := make([]flushable, 0, len(s.head))
	for id, h := range s.head {
		if h.chunk.NumSamples() > 0 {
			full = append(full, flushable{id: id, h: h})
		}
	}
	if err := s.persist(nil, full); err != nil {
		return err
	}
	s.head = make(map[series.ID]*headChunk)
	return nil
}

// Query returns samples matching the request.
func (s *Storage) Query(ctx context.Context, req storage.QueryRequest) ([]metrics.Metric, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Resolve which series match before touching any chunks. This is the point
	// of the index: label matching happens once per series, not once per sample.
	wanted := s.matchingSeries(req)
	if len(wanted) == 0 {
		return nil, nil
	}

	startMs := req.Start.UnixMilli()
	endMs := req.End.UnixMilli()

	var out []metrics.Metric

	s.mu.Lock()
	heads := make(map[series.ID]*headChunk, len(s.head))
	for id, h := range s.head {
		heads[id] = h
	}
	s.mu.Unlock()

	err := s.db.View(func(txn *badger.Txn) error {
		for id, ls := range wanted {
			if err := ctx.Err(); err != nil {
				return err
			}

			name, _ := ls.Get(nameLabel)
			typ, _ := ls.Get(typeLabel)
			userLabels := stripInternal(ls)

			emit := func(tsMs int64, v float64) {
				if tsMs < startMs || tsMs > endMs {
					return
				}
				if req.Limit > 0 && len(out) >= req.Limit {
					return
				}
				out = append(out, metrics.Metric{
					Name:      name,
					Type:      metrics.MetricType(typ),
					Value:     v,
					Labels:    userLabels,
					Timestamp: time.UnixMilli(tsMs),
				})
			}

			if err := s.scanChunks(txn, id, startMs, endMs, emit); err != nil {
				return err
			}
			if h := heads[id]; h != nil && h.chunk.NumSamples() > 0 {
				it := h.chunk.Iterator()
				for it.Next() {
					ts, v := it.At()
					emit(ts, v)
				}
				if err := it.Err(); err != nil {
					return fmt.Errorf("chunked: iterate head chunk: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	if req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// scanChunks decodes every persisted chunk for a series overlapping the range.
func (s *Storage) scanChunks(txn *badger.Txn, id series.ID, startMs, endMs int64, emit func(int64, float64)) error {
	prefix := make([]byte, 9)
	prefix[0] = prefixChunk
	binary.BigEndian.PutUint64(prefix[1:], uint64(id))

	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	it := txn.NewIterator(opts)
	defer it.Close()

	for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
		item := it.Item()
		key := item.KeyCopy(nil)
		chunkStart := chunkKeyTime(key)

		// Chunks are ordered by start time, so once a chunk begins after the
		// window ends nothing further can overlap.
		if chunkStart > endMs {
			break
		}

		err := item.Value(func(val []byte) error {
			c, err := chunkenc.FromBytes(val)
			if err != nil {
				return fmt.Errorf("chunked: decode chunk for series %d: %w", id, err)
			}
			cit := c.Iterator()
			for cit.Next() {
				ts, v := cit.At()
				emit(ts, v)
			}
			return cit.Err()
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// matchingSeries returns the series satisfying the request's name and label
// filters.
func (s *Storage) matchingSeries(req storage.QueryRequest) map[series.ID]series.Labels {
	names := make(map[string]bool, len(req.MetricNames))
	for _, n := range req.MetricNames {
		names[n] = true
	}
	required := series.FromMap(req.Labels)

	out := make(map[series.ID]series.Labels)
	s.index.ForEach(func(id series.ID, ls series.Labels) bool {
		if len(names) > 0 {
			name, _ := ls.Get(nameLabel)
			if !names[name] {
				return true
			}
		}
		if !series.Matches(ls, required) {
			return true
		}
		out[id] = ls
		return true
	})
	return out
}

// stripInternal removes the __name__ and __type__ labels, which are storage
// identity rather than user data.
func stripInternal(ls series.Labels) map[string]string {
	if len(ls) == 0 {
		return nil
	}
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		if l.Name == nameLabel || l.Name == typeLabel {
			continue
		}
		m[l.Name] = l.Value
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// Delete removes chunks that end before opts.Before.
//
// Deletion is chunk-granular. A chunk straddling the cutoff is kept whole
// rather than rewritten, so up to one chunk of data per series can outlive its
// retention window. Rewriting would mean decode, re-encode and re-key on the
// delete path; keeping a bounded overshoot is the cheaper trade, and the bound
// is one chunk.
func (s *Storage) Delete(ctx context.Context, opts storage.DeleteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	beforeMs := opts.Before.UnixMilli()

	var toDelete [][]byte
	err := s.db.View(func(txn *badger.Txn) error {
		iopts := badger.DefaultIteratorOptions
		iopts.Prefix = []byte{prefixChunk}
		it := txn.NewIterator(iopts)
		defer it.Close()

		for it.Rewind(); it.ValidForPrefix([]byte{prefixChunk}); it.Next() {
			item := it.Item()
			key := item.KeyCopy(nil)

			var lastMs int64
			err := item.Value(func(val []byte) error {
				c, err := chunkenc.FromBytes(val)
				if err != nil {
					return err
				}
				cit := c.Iterator()
				for cit.Next() {
					lastMs, _ = cit.At()
				}
				return cit.Err()
			})
			if err != nil {
				return err
			}
			if lastMs < beforeMs {
				toDelete = append(toDelete, key)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	wb := s.db.NewWriteBatch()
	defer wb.Cancel()
	for _, k := range toDelete {
		if err := wb.Delete(k); err != nil {
			return err
		}
	}
	return wb.Flush()
}

// Stats reports store size and contents.
func (s *Storage) Stats(ctx context.Context) (*storage.Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	st := &storage.Stats{TotalSeries: uint64(s.index.Len())}
	lsm, vlog := s.db.Size()
	st.SizeBytes = uint64(lsm + vlog)

	var oldest, newest int64
	first := true

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte{prefixChunk}
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.ValidForPrefix([]byte{prefixChunk}); it.Next() {
			err := it.Item().Value(func(val []byte) error {
				c, err := chunkenc.FromBytes(val)
				if err != nil {
					return err
				}
				st.TotalMetrics += uint64(c.NumSamples())

				cit := c.Iterator()
				for cit.Next() {
					ts, _ := cit.At()
					if first || ts < oldest {
						oldest = ts
					}
					if first || ts > newest {
						newest = ts
					}
					first = false
				}
				return cit.Err()
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	for _, h := range s.head {
		st.TotalMetrics += uint64(h.chunk.NumSamples())
	}
	s.mu.Unlock()

	if !first {
		st.OldestMetric = time.UnixMilli(oldest)
		st.NewestMetric = time.UnixMilli(newest)
	}
	return st, nil
}

// Close flushes open chunks and shuts down the database.
func (s *Storage) Close() error {
	if err := s.Flush(); err != nil {
		s.db.Close()
		return err
	}
	return s.db.Close()
}

// compile-time check that we satisfy the interface.
var _ storage.Storage = (*Storage)(nil)
