// Package tsdb is TinyObs' time series database.
//
// Samples are grouped per series into compressed chunks (see package chunk) of
// at most 120 samples or two hours. Chunks, series definitions and metadata
// live in BadgerDB, which gives us durability (its value log doubles as a
// write-ahead log) and a sorted key space. The label index is held in memory
// and rebuilt on startup, like the head block of a Prometheus server.
//
// Key layout:
//
//	s <id:8>              encoded label set of series id
//	c <id:8> <minT:8>     chunk bytes; the newest chunk of a series is
//	                      rewritten in place until it is full
//	m <metric name>       JSON metadata (type, help, unit)
//
// Samples for a series must arrive in timestamp order. Retention deletes whole
// chunks, so data lives at most one chunk span beyond the configured window.
package tsdb

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"

	"github.com/nicktill/tinyobs/pkg/labels"
	"github.com/nicktill/tinyobs/pkg/tsdb/chunk"
)

const (
	// maxChunkSamples caps samples per chunk: large enough to compress well,
	// small enough that rewriting the head chunk on every commit stays cheap.
	maxChunkSamples = 120
	// maxChunkSpan caps the time range of a chunk. Retention and queries rely
	// on it: a chunk starting at minT holds no sample at or after minT+span.
	maxChunkSpan = int64(2 * time.Hour / time.Millisecond)

	prefixSeries = 's'
	prefixChunk  = 'c'
	prefixMeta   = 'm'
)

// StaleNaN marks the end of a series, for example when a scrape target stops
// exposing it. It is a specific NaN bit pattern, distinct from ordinary NaNs,
// and queries treat it as the absence of a value.
var StaleNaN = math.Float64frombits(0x7ff0000000000002)

// IsStaleNaN reports whether v is the staleness marker.
func IsStaleNaN(v float64) bool { return math.Float64bits(v) == 0x7ff0000000000002 }

// Errors returned for individual samples. Commit aggregates them.
var (
	ErrOutOfOrder     = errors.New("out of order sample")
	ErrDuplicate      = errors.New("duplicate sample for timestamp")
	ErrTooManySeries  = errors.New("series limit reached")
	ErrInvalidLabels  = errors.New("invalid label set")
)

// Options configures a DB.
type Options struct {
	// Dir is the data directory. Ignored when InMemory is set.
	Dir string
	// InMemory keeps everything in RAM. For tests.
	InMemory bool
	// Retention is how long samples are kept. Default 7 days.
	Retention time.Duration
	// MaxSeries caps the number of series; new series beyond it are dropped.
	// It protects the process from label explosions. Default 100,000.
	MaxSeries int
	// MemoryMB bounds Badger's memtables and caches. Default 64.
	MemoryMB int64
	// Now returns the current time. Defaults to time.Now; tests override it.
	Now func() time.Time
}

// Sample is a single timestamped value. T is in milliseconds.
type Sample struct {
	T int64
	V float64
}

// Series is a label set and its samples, in timestamp order.
type Series struct {
	Labels  labels.Labels
	Samples []Sample
}

// Metadata describes a metric family.
type Metadata struct {
	Type string `json:"type"` // counter, gauge, histogram, summary, unknown
	Help string `json:"help,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// DB is a time series database. It is safe for concurrent use.
type DB struct {
	opts Options
	kv   *badger.DB

	mu       sync.RWMutex
	series   map[uint64]*memSeries
	byHash   map[uint64][]*memSeries
	postings map[string]map[string][]uint64 // label name → value → sorted series ids
	meta     map[string]Metadata
	nextID   uint64

	// commitMu serializes commits so head chunk writes reach Badger in the
	// same order they were produced.
	commitMu sync.Mutex

	stats struct {
		sync.Mutex
		appended, rejected uint64
	}
}

type memSeries struct {
	id   uint64
	lset labels.Labels

	mu        sync.Mutex
	firstT    int64 // oldest sample on disk
	lastT     int64 // newest sample; math.MinInt64 if none
	lastV     float64
	headMinT  int64
	head      *chunk.Chunk
	app       *chunk.Appender
	headDirty bool
}

// Open opens or creates a database.
func Open(opts Options) (*DB, error) {
	if opts.Retention <= 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	if opts.MaxSeries <= 0 {
		opts.MaxSeries = 100_000
	}
	if opts.MemoryMB <= 0 {
		opts.MemoryMB = 64
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	bo := badger.DefaultOptions(opts.Dir).
		WithInMemory(opts.InMemory).
		WithLogger(nil).
		WithNumVersionsToKeep(1).
		WithMemTableSize(opts.MemoryMB << 20 / 4).
		WithNumMemtables(2).
		WithBlockCacheSize(opts.MemoryMB << 20 / 4).
		WithIndexCacheSize(opts.MemoryMB << 20 / 8).
		WithValueThreshold(1 << 10) // chunks are small; keep them in the LSM tree
	if opts.InMemory {
		bo.Dir, bo.ValueDir = "", ""
	}
	kv, err := badger.Open(bo)
	if err != nil {
		return nil, fmt.Errorf("open badger: %w", err)
	}

	db := &DB{
		opts:     opts,
		kv:       kv,
		series:   map[uint64]*memSeries{},
		byHash:   map[uint64][]*memSeries{},
		postings: map[string]map[string][]uint64{},
		meta:     map[string]Metadata{},
		nextID:   1,
	}
	if err := db.load(); err != nil {
		kv.Close()
		return nil, err
	}
	return db, nil
}

// Close flushes and closes the database.
func (db *DB) Close() error { return db.kv.Close() }

// load rebuilds the in-memory index and head chunks from disk.
func (db *DB) load() error {
	return db.kv.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte{prefixSeries}
		it := txn.NewIterator(opts)
		for it.Rewind(); it.Valid(); it.Next() {
			id := binary.BigEndian.Uint64(it.Item().Key()[1:])
			var lset labels.Labels
			if err := it.Item().Value(func(v []byte) (err error) {
				lset, err = decodeLabels(v)
				return err
			}); err != nil {
				it.Close()
				return fmt.Errorf("series %d: %w", id, err)
			}
			db.indexSeries(&memSeries{id: id, lset: lset, lastT: math.MinInt64, firstT: math.MaxInt64})
			if id >= db.nextID {
				db.nextID = id + 1
			}
		}
		it.Close()

		for _, s := range db.series {
			if err := db.loadHead(txn, s); err != nil {
				return fmt.Errorf("series %d: %w", s.id, err)
			}
		}

		opts = badger.DefaultIteratorOptions
		opts.Prefix = []byte{prefixMeta}
		it = txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			var md Metadata
			if err := it.Item().Value(func(v []byte) error { return json.Unmarshal(v, &md) }); err != nil {
				return err
			}
			db.meta[string(it.Item().Key()[1:])] = md
		}
		return nil
	})
}

// loadHead reads a series' first chunk key and its last chunk, which becomes
// the head chunk that new samples are appended to.
func (db *DB) loadHead(txn *badger.Txn, s *memSeries) error {
	prefix := chunkPrefix(s.id)

	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	opts.PrefetchValues = false
	it := txn.NewIterator(opts)
	it.Rewind()
	if it.Valid() {
		s.firstT = chunkKeyMinT(it.Item().Key())
	}
	it.Close()

	opts.Reverse = true
	opts.PrefetchValues = true
	it = txn.NewIterator(opts)
	defer it.Close()
	it.Seek(append(append([]byte{}, prefix...), 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff))
	if !it.Valid() {
		return nil
	}
	b, err := it.Item().ValueCopy(nil)
	if err != nil {
		return err
	}
	c, err := chunk.FromBytes(b)
	if err != nil {
		return err
	}
	app, err := c.Appender()
	if err != nil {
		return err
	}
	cit := c.Iterator()
	for cit.Next() {
		s.lastT, s.lastV = cit.At()
	}
	s.head, s.app = c, app
	s.headMinT = chunkKeyMinT(it.Item().Key())
	return nil
}

// indexSeries adds s to the in-memory maps. Caller holds db.mu for writing.
func (db *DB) indexSeries(s *memSeries) {
	db.series[s.id] = s
	h := s.lset.Hash()
	db.byHash[h] = append(db.byHash[h], s)
	for _, l := range s.lset {
		vals := db.postings[l.Name]
		if vals == nil {
			vals = map[string][]uint64{}
			db.postings[l.Name] = vals
		}
		ids := vals[l.Value]
		i := sort.Search(len(ids), func(i int) bool { return ids[i] >= s.id })
		ids = append(ids, 0)
		copy(ids[i+1:], ids[i:])
		ids[i] = s.id
		vals[l.Value] = ids
	}
}

// unindexSeries removes s from the in-memory maps. Caller holds db.mu.
func (db *DB) unindexSeries(s *memSeries) {
	delete(db.series, s.id)
	h := s.lset.Hash()
	list := db.byHash[h]
	for i, o := range list {
		if o == s {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(db.byHash, h)
	} else {
		db.byHash[h] = list
	}
	for _, l := range s.lset {
		ids := db.postings[l.Name][l.Value]
		i := sort.Search(len(ids), func(i int) bool { return ids[i] >= s.id })
		if i < len(ids) && ids[i] == s.id {
			ids = append(ids[:i], ids[i+1:]...)
		}
		if len(ids) == 0 {
			delete(db.postings[l.Name], l.Value)
			if len(db.postings[l.Name]) == 0 {
				delete(db.postings, l.Name)
			}
		} else {
			db.postings[l.Name][l.Value] = ids
		}
	}
}

func (db *DB) lookup(lset labels.Labels) *memSeries {
	for _, s := range db.byHash[lset.Hash()] {
		if labels.Equal(s.lset, lset) {
			return s
		}
	}
	return nil
}

// Appender batches samples for a single Commit.
type Appender struct {
	db      *DB
	samples []pending
}

type pending struct {
	lset labels.Labels
	t    int64
	v    float64
}

// Appender returns a new appender.
func (db *DB) Appender() *Appender { return &Appender{db: db} }

// Append queues a sample. The label set must be sorted and contain __name__.
func (a *Appender) Append(lset labels.Labels, t int64, v float64) {
	a.samples = append(a.samples, pending{lset, t, v})
}

// CommitResult reports what happened to a committed batch.
type CommitResult struct {
	Appended   int
	OutOfOrder int
	Duplicates int
	SeriesCap  int // dropped because MaxSeries was reached
	Invalid    int
}

// Rejected returns the number of samples not stored.
func (r CommitResult) Rejected() int { return r.OutOfOrder + r.SeriesCap + r.Invalid }

// Err summarizes rejected samples as an error, or nil if all were stored.
// Exact duplicates are not an error: re-sending a sample is harmless.
func (r CommitResult) Err() error {
	if r.Rejected() == 0 {
		return nil
	}
	var errs []error
	if r.OutOfOrder > 0 {
		errs = append(errs, fmt.Errorf("%d samples: %w", r.OutOfOrder, ErrOutOfOrder))
	}
	if r.SeriesCap > 0 {
		errs = append(errs, fmt.Errorf("%d samples: %w", r.SeriesCap, ErrTooManySeries))
	}
	if r.Invalid > 0 {
		errs = append(errs, fmt.Errorf("%d samples: %w", r.Invalid, ErrInvalidLabels))
	}
	return errors.Join(errs...)
}

// Commit writes the queued samples. Samples that cannot be stored are counted
// in the result rather than failing the batch; the returned error is reserved
// for storage failures.
func (a *Appender) Commit() (CommitResult, error) {
	db := a.db
	var res CommitResult

	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	wb := db.kv.NewWriteBatch()
	defer wb.Cancel()

	dirty := map[*memSeries]struct{}{}
	for _, p := range a.samples {
		s, created, err := db.getOrCreate(p.lset)
		if err != nil {
			if errors.Is(err, ErrTooManySeries) {
				res.SeriesCap++
			} else {
				res.Invalid++
			}
			continue
		}
		if created {
			if err := wb.Set(seriesKey(s.id), encodeLabels(s.lset)); err != nil {
				return res, err
			}
		}

		s.mu.Lock()
		switch {
		case p.t < s.lastT || (p.t == s.lastT && math.Float64bits(p.v) != math.Float64bits(s.lastV)):
			res.OutOfOrder++
		case p.t == s.lastT:
			res.Duplicates++
		default:
			if s.head == nil || s.head.NumSamples() >= maxChunkSamples || p.t-s.headMinT >= maxChunkSpan {
				if s.headDirty {
					// Persist the finished chunk before cutting a new one.
					if err := wb.Set(chunkKey(s.id, s.headMinT), clone(s.head.Bytes())); err != nil {
						s.mu.Unlock()
						return res, err
					}
				}
				s.head = chunk.New()
				s.app, _ = s.head.Appender()
				s.headMinT = p.t
			}
			s.app.Append(p.t, p.v)
			if s.firstT > p.t {
				s.firstT = p.t
			}
			s.lastT, s.lastV = p.t, p.v
			s.headDirty = true
			dirty[s] = struct{}{}
			res.Appended++
		}
		s.mu.Unlock()
	}

	for s := range dirty {
		s.mu.Lock()
		err := wb.Set(chunkKey(s.id, s.headMinT), clone(s.head.Bytes()))
		s.headDirty = false
		s.mu.Unlock()
		if err != nil {
			return res, err
		}
	}
	if err := wb.Flush(); err != nil {
		return res, err
	}

	db.stats.Lock()
	db.stats.appended += uint64(res.Appended)
	db.stats.rejected += uint64(res.Rejected())
	db.stats.Unlock()
	a.samples = a.samples[:0]
	return res, nil
}

func (db *DB) getOrCreate(lset labels.Labels) (*memSeries, bool, error) {
	db.mu.RLock()
	s := db.lookup(lset)
	db.mu.RUnlock()
	if s != nil {
		return s, false, nil
	}
	if err := validateLabels(lset); err != nil {
		return nil, false, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if s := db.lookup(lset); s != nil {
		return s, false, nil
	}
	if len(db.series) >= db.opts.MaxSeries {
		return nil, false, ErrTooManySeries
	}
	s = &memSeries{id: db.nextID, lset: lset, lastT: math.MinInt64, firstT: math.MaxInt64}
	db.nextID++
	db.indexSeries(s)
	return s, true, nil
}

func validateLabels(lset labels.Labels) error {
	if lset.Get(labels.MetricName) == "" {
		return fmt.Errorf("%w: missing metric name", ErrInvalidLabels)
	}
	for i, l := range lset {
		if l.Name == "" || l.Value == "" {
			return fmt.Errorf("%w: empty label name or value", ErrInvalidLabels)
		}
		if i > 0 && lset[i-1].Name >= l.Name {
			return fmt.Errorf("%w: labels not sorted or duplicated", ErrInvalidLabels)
		}
	}
	return nil
}

// SetMetadata records metadata for a metric family.
func (db *DB) SetMetadata(metric string, md Metadata) error {
	db.mu.Lock()
	if db.meta[metric] == md {
		db.mu.Unlock()
		return nil
	}
	db.meta[metric] = md
	db.mu.Unlock()

	b, _ := json.Marshal(md)
	return db.kv.Update(func(txn *badger.Txn) error {
		return txn.Set(append([]byte{prefixMeta}, metric...), b)
	})
}

// Metadata returns metadata for all metric families that reported it.
func (db *DB) Metadata() map[string]Metadata {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make(map[string]Metadata, len(db.meta))
	for k, v := range db.meta {
		out[k] = v
	}
	return out
}

// Select returns all series matching ms with their samples in [mint, maxt].
// Series without samples in the range are omitted. Results are sorted by labels.
// maxSamples bounds the total samples loaded (0 = unlimited).
func (db *DB) Select(ctx context.Context, mint, maxt int64, maxSamples int, ms ...*labels.Matcher) ([]Series, error) {
	targets := db.matchingSeries(mint, maxt, ms)

	var out []Series
	total := 0
	err := db.kv.View(func(txn *badger.Txn) error {
		for _, t := range targets {
			if err := ctx.Err(); err != nil {
				return err
			}
			samples, err := readSamples(txn, t.id, mint, maxt)
			if err != nil {
				return fmt.Errorf("series %s: %w", t.lset, err)
			}
			if len(samples) == 0 {
				continue
			}
			total += len(samples)
			if maxSamples > 0 && total > maxSamples {
				return fmt.Errorf("query would load more than %d samples; narrow the time range or selector", maxSamples)
			}
			out = append(out, Series{Labels: t.lset, Samples: samples})
		}
		return nil
	})
	return out, err
}

type seriesRef struct {
	id   uint64
	lset labels.Labels
}

// matchingSeries resolves matchers against the index and returns series whose
// data may overlap [mint, maxt], sorted by labels.
func (db *DB) matchingSeries(mint, maxt int64, ms []*labels.Matcher) []seriesRef {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var candidates []uint64
	haveCandidates := false
	for _, m := range ms {
		if m.MatchesEmpty() {
			continue // cannot narrow the search via postings
		}
		var ids []uint64
		for v, p := range db.postings[m.Name] {
			if m.Matches(v) {
				ids = union(ids, p)
			}
		}
		if !haveCandidates || len(ids) < len(candidates) {
			candidates, haveCandidates = ids, true
		}
	}
	if !haveCandidates {
		for id := range db.series {
			candidates = append(candidates, id)
		}
	}

	var out []seriesRef
	for _, id := range candidates {
		s := db.series[id]
		if s == nil || !labels.MatchLabels(s.lset, ms...) {
			continue
		}
		s.mu.Lock()
		first, last := s.firstT, s.lastT
		s.mu.Unlock()
		if last < mint || first > maxt {
			continue
		}
		out = append(out, seriesRef{s.id, s.lset})
	}
	sort.Slice(out, func(i, j int) bool { return labels.Compare(out[i].lset, out[j].lset) < 0 })
	return out
}

func readSamples(txn *badger.Txn, id uint64, mint, maxt int64) ([]Sample, error) {
	prefix := chunkPrefix(id)
	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	opts.PrefetchSize = 16
	it := txn.NewIterator(opts)
	defer it.Close()

	var out []Sample
	// A chunk starting before mint-span cannot contain samples >= mint.
	seek := prefix
	if mint > math.MinInt64+maxChunkSpan {
		seek = chunkKey(id, mint-maxChunkSpan+1)
	}
	for it.Seek(seek); it.Valid(); it.Next() {
		if chunkKeyMinT(it.Item().Key()) > maxt {
			break
		}
		err := it.Item().Value(func(b []byte) error {
			c, err := chunk.FromBytes(b)
			if err != nil {
				return err
			}
			ci := c.Iterator()
			for ci.Next() {
				t, v := ci.At()
				if t < mint {
					continue
				}
				if t > maxt {
					break
				}
				out = append(out, Sample{t, v})
			}
			return ci.Err()
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Series returns the label sets of series matching ms with data in [mint, maxt].
func (db *DB) Series(mint, maxt int64, ms ...*labels.Matcher) []labels.Labels {
	refs := db.matchingSeries(mint, maxt, ms)
	out := make([]labels.Labels, len(refs))
	for i, r := range refs {
		out[i] = r.lset
	}
	return out
}

// LabelNames returns sorted label names of series matching ms (all series if none).
func (db *DB) LabelNames(mint, maxt int64, ms ...*labels.Matcher) []string {
	set := map[string]struct{}{}
	if len(ms) == 0 {
		db.mu.RLock()
		for n := range db.postings {
			set[n] = struct{}{}
		}
		db.mu.RUnlock()
	} else {
		for _, ls := range db.Series(mint, maxt, ms...) {
			for _, l := range ls {
				set[l.Name] = struct{}{}
			}
		}
	}
	return sortedKeys(set)
}

// LabelValues returns sorted values of a label across series matching ms.
func (db *DB) LabelValues(name string, mint, maxt int64, ms ...*labels.Matcher) []string {
	set := map[string]struct{}{}
	if len(ms) == 0 {
		db.mu.RLock()
		for v := range db.postings[name] {
			set[v] = struct{}{}
		}
		db.mu.RUnlock()
	} else {
		for _, ls := range db.Series(mint, maxt, ms...) {
			if v := ls.Get(name); v != "" {
				set[v] = struct{}{}
			}
		}
	}
	return sortedKeys(set)
}

// Stats describes the database.
type Stats struct {
	NumSeries        int   `json:"numSeries"`
	NumMetrics       int   `json:"numMetrics"`
	MinTime          int64 `json:"minTime"` // ms; 0 if empty
	MaxTime          int64 `json:"maxTime"`
	DiskBytes        int64 `json:"diskBytes"`
	SamplesAppended  uint64 `json:"samplesAppended"` // since start
	SamplesRejected  uint64 `json:"samplesRejected"`
	RetentionSeconds int64 `json:"retentionSeconds"`
	MaxSeries        int   `json:"maxSeries"`
}

// Stats returns current statistics.
func (db *DB) Stats() Stats {
	db.mu.RLock()
	st := Stats{
		NumSeries:        len(db.series),
		NumMetrics:       len(db.postings[labels.MetricName]),
		RetentionSeconds: int64(db.opts.Retention / time.Second),
		MaxSeries:        db.opts.MaxSeries,
	}
	minT, maxT := int64(math.MaxInt64), int64(math.MinInt64)
	for _, s := range db.series {
		s.mu.Lock()
		if s.firstT < minT {
			minT = s.firstT
		}
		if s.lastT > maxT {
			maxT = s.lastT
		}
		s.mu.Unlock()
	}
	db.mu.RUnlock()
	if st.NumSeries > 0 && maxT != math.MinInt64 {
		st.MinTime, st.MaxTime = minT, maxT
	}
	lsm, vlog := db.kv.Size()
	st.DiskBytes = lsm + vlog
	db.stats.Lock()
	st.SamplesAppended, st.SamplesRejected = db.stats.appended, db.stats.rejected
	db.stats.Unlock()
	return st
}

// MetricCardinality is the series count of one metric and its labels.
type MetricCardinality struct {
	Metric string         `json:"metric"`
	Series int            `json:"series"`
	Labels map[string]int `json:"labels"` // label name → distinct values
}

// Cardinality returns per-metric series counts, largest first.
func (db *DB) Cardinality() []MetricCardinality {
	db.mu.RLock()
	defer db.mu.RUnlock()
	var out []MetricCardinality
	for name, ids := range db.postings[labels.MetricName] {
		mc := MetricCardinality{Metric: name, Series: len(ids), Labels: map[string]int{}}
		values := map[string]map[string]struct{}{}
		for _, id := range ids {
			for _, l := range db.series[id].lset {
				if l.Name == labels.MetricName {
					continue
				}
				if values[l.Name] == nil {
					values[l.Name] = map[string]struct{}{}
				}
				values[l.Name][l.Value] = struct{}{}
			}
		}
		for n, vs := range values {
			mc.Labels[n] = len(vs)
		}
		out = append(out, mc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Series != out[j].Series {
			return out[i].Series > out[j].Series
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

// ApplyRetention deletes data older than the retention window: whole chunks,
// and series with no remaining samples. Then it reclaims disk space.
func (db *DB) ApplyRetention() error {
	cutoff := db.opts.Now().Add(-db.opts.Retention).UnixMilli()

	db.mu.RLock()
	all := make([]*memSeries, 0, len(db.series))
	for _, s := range db.series {
		all = append(all, s)
	}
	db.mu.RUnlock()

	// Hold the commit lock so no appends race with deletions.
	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	wb := db.kv.NewWriteBatch()
	defer wb.Cancel()
	var dead []*memSeries
	err := db.kv.View(func(txn *badger.Txn) error {
		for _, s := range all {
			s.mu.Lock()
			drop := s.lastT < cutoff
			s.mu.Unlock()

			opts := badger.DefaultIteratorOptions
			opts.Prefix = chunkPrefix(s.id)
			opts.PrefetchValues = false
			it := txn.NewIterator(opts)
			newFirst := int64(math.MaxInt64)
			for it.Rewind(); it.Valid(); it.Next() {
				minT := chunkKeyMinT(it.Item().Key())
				if drop || minT+maxChunkSpan <= cutoff {
					if err := wb.Delete(it.Item().KeyCopy(nil)); err != nil {
						it.Close()
						return err
					}
					continue
				}
				newFirst = minT
				break
			}
			it.Close()

			if drop {
				if err := wb.Delete(seriesKey(s.id)); err != nil {
					return err
				}
				dead = append(dead, s)
			} else {
				s.mu.Lock()
				if newFirst != math.MaxInt64 && newFirst > s.firstT {
					s.firstT = newFirst
				}
				s.mu.Unlock()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := wb.Flush(); err != nil {
		return err
	}

	db.mu.Lock()
	for _, s := range dead {
		db.unindexSeries(s)
	}
	db.mu.Unlock()

	if !db.opts.InMemory {
		for db.kv.RunValueLogGC(0.5) == nil {
		}
	}
	return nil
}

// Keys and encodings.

func seriesKey(id uint64) []byte {
	k := make([]byte, 9)
	k[0] = prefixSeries
	binary.BigEndian.PutUint64(k[1:], id)
	return k
}

func chunkPrefix(id uint64) []byte {
	k := make([]byte, 9)
	k[0] = prefixChunk
	binary.BigEndian.PutUint64(k[1:], id)
	return k
}

func chunkKey(id uint64, minT int64) []byte {
	k := make([]byte, 17)
	k[0] = prefixChunk
	binary.BigEndian.PutUint64(k[1:], id)
	// Flip the sign bit so negative timestamps sort before positive ones.
	binary.BigEndian.PutUint64(k[9:], uint64(minT)^(1<<63))
	return k
}

func chunkKeyMinT(k []byte) int64 {
	return int64(binary.BigEndian.Uint64(k[9:]) ^ (1 << 63))
}

func encodeLabels(ls labels.Labels) []byte {
	b := binary.AppendUvarint(nil, uint64(len(ls)))
	for _, l := range ls {
		b = binary.AppendUvarint(b, uint64(len(l.Name)))
		b = append(b, l.Name...)
		b = binary.AppendUvarint(b, uint64(len(l.Value)))
		b = append(b, l.Value...)
	}
	return b
}

func decodeLabels(b []byte) (labels.Labels, error) {
	readString := func() (string, error) {
		n, k := binary.Uvarint(b)
		if k <= 0 || int(n) > len(b)-k {
			return "", errors.New("corrupt label encoding")
		}
		s := string(b[k : k+int(n)])
		b = b[k+int(n):]
		return s, nil
	}
	n, k := binary.Uvarint(b)
	if k <= 0 {
		return nil, errors.New("corrupt label encoding")
	}
	b = b[k:]
	ls := make(labels.Labels, 0, n)
	for i := uint64(0); i < n; i++ {
		name, err := readString()
		if err != nil {
			return nil, err
		}
		value, err := readString()
		if err != nil {
			return nil, err
		}
		ls = append(ls, labels.Label{Name: name, Value: value})
	}
	return ls, nil
}

func union(a, b []uint64) []uint64 {
	out := make([]uint64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
