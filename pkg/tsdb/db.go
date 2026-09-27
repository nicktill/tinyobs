// Package tsdb stores time series samples in BadgerDB.
//
// Each sample is one key: the series id and timestamp, pointing at the 8-byte
// value. Badger compresses blocks with ZSTD, which brings a sample to about
// 14 bytes on disk. The label index lives in memory and is rebuilt at startup.
// ADR 0001 records why this layout was chosen over compressed chunks.
//
// Key layout:
//
//	s <id:8>              encoded label set
//	d <id:8> <t:8>        float64 bits; t is milliseconds with the sign bit
//	                      flipped so keys sort in time order
//	m <metric name>       JSON metadata
//
// A sample is durable once Commit returns: Badger's value log is the
// write-ahead log.
package tsdb

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"

	"github.com/nicktill/tinyobs/pkg/labels"
)

const (
	prefixSeries = 's'
	prefixData   = 'd'
	prefixMeta   = 'm'
)

// StaleNaN marks the end of a series, for example when a scrape target stops
// exposing it. It is a specific NaN bit pattern, distinct from ordinary NaNs,
// and queries treat it as the absence of a value.
var StaleNaN = math.Float64frombits(staleNaNBits)

const staleNaNBits = 0x7ff0000000000002

// IsStaleNaN reports whether v is the staleness marker.
func IsStaleNaN(v float64) bool { return math.Float64bits(v) == staleNaNBits }

// ErrTooManySeries is returned when a new series would exceed MaxSeries.
var ErrTooManySeries = errors.New("series limit reached")

// ErrTooManyMetricSeries is returned when a new series would exceed
// MaxSeriesPerMetric.
var ErrTooManyMetricSeries = errors.New("per-metric series limit reached")

// Options configures a DB.
type Options struct {
	// Dir is the data directory. Ignored when InMemory is set.
	Dir string
	// InMemory keeps everything in RAM. For tests.
	InMemory bool
	// Retention is how long samples are kept. Default 72 hours.
	Retention time.Duration
	// MaxSeries caps the number of series. Samples for new series beyond it
	// are rejected, which bounds memory and disk. Default 50,000.
	MaxSeries int
	// MaxSeriesPerMetric caps the series of one metric name, so a single
	// label explosion (a user ID in a label, say) is rejected on its own
	// instead of exhausting MaxSeries for every other metric. Default 10,000.
	MaxSeriesPerMetric int
	// MemoryMB bounds Badger's memtables and caches. Default 64.
	MemoryMB int64
	// Now returns the current time. Tests override it.
	Now func() time.Time
}

// Sample is a single value. T is milliseconds since the Unix epoch.
type Sample struct {
	T int64
	V float64
}

// Series is a label set and its samples in timestamp order.
type Series struct {
	Labels  labels.Labels
	Samples []Sample
}

// Metadata describes a metric family.
type Metadata struct {
	Type string `json:"type"` // counter, gauge, histogram, summary or unknown
	Help string `json:"help,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// DB is a time series database, safe for concurrent use.
type DB struct {
	opts Options
	kv   *badger.DB

	mu       sync.RWMutex
	series   map[uint64]*memSeries
	byHash   map[uint64][]*memSeries
	postings map[string]map[string][]uint64 // label name → value → sorted series ids
	meta     map[string]Metadata

	// commitMu serializes commits and retention, so the in-memory view of
	// each series' newest sample always matches what is on disk.
	commitMu sync.Mutex

	counters struct {
		sync.Mutex
		appended uint64
		rejected map[string]uint64 // by reason
	}
}

type memSeries struct {
	id   uint64
	lset labels.Labels

	mu     sync.Mutex
	firstT int64 // oldest stored sample
	lastT  int64 // newest stored sample; math.MinInt64 when empty
	lastV  float64
}

// Open opens or creates a database.
func Open(opts Options) (*DB, error) {
	if opts.Retention <= 0 {
		opts.Retention = 72 * time.Hour
	}
	if opts.MaxSeries <= 0 {
		opts.MaxSeries = 50_000
	}
	if opts.MaxSeriesPerMetric <= 0 {
		opts.MaxSeriesPerMetric = 10_000
	}
	if opts.MemoryMB <= 0 {
		opts.MemoryMB = 64
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	mem := opts.MemoryMB << 20
	bo := badger.DefaultOptions(opts.Dir).
		WithLogger(nil).
		WithCompression(options.ZSTD).
		WithZSTDCompressionLevel(3).
		WithNumVersionsToKeep(1).
		WithMemTableSize(mem / 4).
		WithNumMemtables(2).
		WithBlockCacheSize(mem / 4).
		WithIndexCacheSize(mem / 8).
		// Values (8 bytes) live inline in the LSM tree, so the value log only
		// carries write-ahead records. Badger preallocates each value log
		// file at full size; the 2 GB default would dominate disk usage.
		WithValueLogFileSize(64 << 20)
	if opts.InMemory {
		bo = bo.WithInMemory(true).WithDir("").WithValueDir("")
	}
	kv, err := badger.Open(bo)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	db := &DB{
		opts:     opts,
		kv:       kv,
		series:   map[uint64]*memSeries{},
		byHash:   map[uint64][]*memSeries{},
		postings: map[string]map[string][]uint64{},
		meta:     map[string]Metadata{},
	}
	db.counters.rejected = map[string]uint64{}
	if err := db.load(); err != nil {
		kv.Close()
		return nil, fmt.Errorf("load storage: %w", err)
	}
	return db, nil
}

// Close flushes and closes the database.
func (db *DB) Close() error { return db.kv.Close() }

// load rebuilds the in-memory index from disk: every series definition, plus
// the first and last sample of each series (two key seeks per series).
func (db *DB) load() error {
	return db.kv.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte{prefixSeries}
		it := txn.NewIterator(opts)
		for it.Rewind(); it.Valid(); it.Next() {
			id := binary.BigEndian.Uint64(it.Item().Key()[1:])
			var lset labels.Labels
			err := it.Item().Value(func(v []byte) (err error) {
				lset, err = decodeLabels(v)
				return err
			})
			if err != nil {
				it.Close()
				return fmt.Errorf("series %d: %w", id, err)
			}
			db.indexSeries(&memSeries{id: id, lset: lset, firstT: math.MaxInt64, lastT: math.MinInt64})
		}
		it.Close()

		fwd := badger.DefaultIteratorOptions
		fwd.PrefetchValues = false
		rev := badger.DefaultIteratorOptions
		rev.PrefetchValues = false
		rev.Reverse = true
		for _, s := range db.series {
			prefix := dataPrefix(s.id)
			fwd.Prefix, rev.Prefix = prefix, prefix

			it := txn.NewIterator(fwd)
			it.Rewind()
			if it.Valid() {
				s.firstT = dataKeyTime(it.Item().Key())
			}
			it.Close()

			it = txn.NewIterator(rev)
			it.Seek(dataKey(s.id, math.MaxInt64))
			if it.Valid() {
				s.lastT = dataKeyTime(it.Item().Key())
				if err := it.Item().Value(func(v []byte) error {
					s.lastV = decodeValue(v)
					return nil
				}); err != nil {
					it.Close()
					return err
				}
			}
			it.Close()
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

// indexSeries adds s to the in-memory index. Caller holds db.mu or is load.
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
		vals[l.Value] = insertSorted(vals[l.Value], s.id)
	}
}

// unindexSeries removes s from the in-memory index. Caller holds db.mu.
func (db *DB) unindexSeries(s *memSeries) {
	delete(db.series, s.id)
	h := s.lset.Hash()
	list := db.byHash[h]
	for i, o := range list {
		if o == s {
			list = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(db.byHash, h)
	} else {
		db.byHash[h] = list
	}
	for _, l := range s.lset {
		ids := removeSorted(db.postings[l.Name][l.Value], s.id)
		if len(ids) > 0 {
			db.postings[l.Name][l.Value] = ids
			continue
		}
		delete(db.postings[l.Name], l.Value)
		if len(db.postings[l.Name]) == 0 {
			delete(db.postings, l.Name)
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

// allocateID derives an id from the label hash, so ids are well spread over
// the key space, and probes forward on the (astronomically rare) collision.
// Caller holds db.mu. Adapted from the series index in #37.
func (db *DB) allocateID(lset labels.Labels) uint64 {
	id := lset.Hash()
	for {
		if _, taken := db.series[id]; !taken {
			return id
		}
		id++
	}
}

// Appender collects samples for one atomic Commit.
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

// Append queues a sample. The label set must be sorted (see labels.New).
func (a *Appender) Append(lset labels.Labels, t int64, v float64) {
	a.samples = append(a.samples, pending{lset, t, v})
}

// Rejection reasons, used in CommitResult and in the rejected-sample counters.
const (
	ReasonOutOfOrder = "out_of_order"
	ReasonSeriesCap  = "series_limit"
	ReasonMetricCap  = "metric_series_limit"
	ReasonInvalid    = "invalid_labels"
)

// CommitResult reports what happened to each queued sample.
type CommitResult struct {
	Appended   int
	Duplicates int            // identical to a stored sample; ignored
	Rejected   map[string]int // by reason
	// FirstError describes one rejected sample, for error messages.
	FirstError error
}

// NumRejected returns the number of samples that were not stored.
func (r CommitResult) NumRejected() int {
	n := 0
	for _, c := range r.Rejected {
		n += c
	}
	return n
}

func (r *CommitResult) reject(reason string, err error) {
	if r.Rejected == nil {
		r.Rejected = map[string]int{}
	}
	r.Rejected[reason]++
	if r.FirstError == nil {
		r.FirstError = err
	}
}

// Commit stores the queued samples. Per-sample problems (out of order, series
// limit, invalid labels) are reported in the result and do not fail the batch.
// The returned error means nothing was stored.
func (a *Appender) Commit() (CommitResult, error) {
	db := a.db
	res := CommitResult{}
	defer func() { a.samples = a.samples[:0] }()

	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	type undo struct {
		lastT, firstT int64
		lastV         float64
	}
	touched := map[*memSeries]undo{}
	var created []*memSeries

	wb := db.kv.NewWriteBatch()
	defer wb.Cancel()

	rollback := func() {
		for s, u := range touched {
			s.mu.Lock()
			s.lastT, s.lastV, s.firstT = u.lastT, u.lastV, u.firstT
			s.mu.Unlock()
		}
		db.mu.Lock()
		for _, s := range created {
			db.unindexSeries(s)
		}
		db.mu.Unlock()
	}

	for _, p := range a.samples {
		s, isNew, err := db.getOrCreate(p.lset)
		switch {
		case errors.Is(err, ErrTooManySeries):
			res.reject(ReasonSeriesCap, fmt.Errorf("%s: %w (%d)", p.lset, err, db.opts.MaxSeries))
			continue
		case errors.Is(err, ErrTooManyMetricSeries):
			res.reject(ReasonMetricCap, fmt.Errorf("%s: %w (%d)", p.lset, err, db.opts.MaxSeriesPerMetric))
			continue
		case err != nil:
			res.reject(ReasonInvalid, err)
			continue
		}
		if isNew {
			created = append(created, s)
			if err := wb.Set(seriesKey(s.id), encodeLabels(s.lset)); err != nil {
				rollback()
				return CommitResult{}, err
			}
		}

		s.mu.Lock()
		switch {
		case p.t < s.lastT || (p.t == s.lastT && math.Float64bits(p.v) != math.Float64bits(s.lastV)):
			s.mu.Unlock()
			res.reject(ReasonOutOfOrder, fmt.Errorf("%s: sample at %d is older than or conflicts with stored sample at %d", s.lset, p.t, s.lastT))
			continue
		case p.t == s.lastT:
			s.mu.Unlock()
			res.Duplicates++
			continue
		}
		if _, ok := touched[s]; !ok {
			touched[s] = undo{s.lastT, s.firstT, s.lastV}
		}
		s.lastT, s.lastV = p.t, p.v
		if p.t < s.firstT {
			s.firstT = p.t
		}
		s.mu.Unlock()

		if err := wb.Set(dataKey(s.id, p.t), encodeValue(p.v)); err != nil {
			rollback()
			return CommitResult{}, err
		}
		res.Appended++
	}

	if err := wb.Flush(); err != nil {
		rollback()
		return CommitResult{}, fmt.Errorf("write samples: %w", err)
	}

	db.counters.Lock()
	db.counters.appended += uint64(res.Appended)
	for r, n := range res.Rejected {
		db.counters.rejected[r] += uint64(n)
	}
	db.counters.Unlock()
	return res, nil
}

func (db *DB) getOrCreate(lset labels.Labels) (*memSeries, bool, error) {
	db.mu.RLock()
	s := db.lookup(lset)
	db.mu.RUnlock()
	if s != nil {
		return s, false, nil
	}
	if err := labels.Validate(lset); err != nil {
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
	if len(db.postings[labels.MetricName][lset.Get(labels.MetricName)]) >= db.opts.MaxSeriesPerMetric {
		return nil, false, ErrTooManyMetricSeries
	}
	s = &memSeries{id: db.allocateID(lset), lset: lset, firstT: math.MaxInt64, lastT: math.MinInt64}
	db.indexSeries(s)
	return s, true, nil
}

// SetMetadata records metadata for a metric family. Unchanged metadata is not
// rewritten, so callers may set it on every scrape.
func (db *DB) SetMetadata(metric string, md Metadata) error {
	db.mu.Lock()
	if old, ok := db.meta[metric]; ok && old == md {
		db.mu.Unlock()
		return nil
	}
	db.meta[metric] = md
	db.mu.Unlock()

	b, err := json.Marshal(md)
	if err != nil {
		return err
	}
	return db.kv.Update(func(txn *badger.Txn) error {
		return txn.Set(append([]byte{prefixMeta}, metric...), b)
	})
}

// Metadata returns metadata for every metric family that reported it.
func (db *DB) Metadata() map[string]Metadata {
	db.mu.RLock()
	defer db.mu.RUnlock()
	out := make(map[string]Metadata, len(db.meta))
	for k, v := range db.meta {
		out[k] = v
	}
	return out
}

// ErrSampleLimit is returned by Select when a query would load too many samples.
var ErrSampleLimit = errors.New("query would load too many samples")

// Select returns the series matching all matchers, with their samples in
// [mint, maxt], sorted by labels. Series with no samples in the range are
// omitted. maxSamples bounds the total loaded (0 = no limit).
func (db *DB) Select(ctx context.Context, mint, maxt int64, maxSamples int, ms ...*labels.Matcher) ([]Series, error) {
	refs := db.matchingSeries(mint, maxt, ms)
	var out []Series
	total := 0
	err := db.kv.View(func(txn *badger.Txn) error {
		// One iterator for the whole query, positioned per series by Seek.
		// Values are 8 bytes stored inline in the LSM tree, so Badger's value
		// prefetching (a goroutine per item) would only add overhead.
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = []byte{prefixData}
		it := txn.NewIterator(opts)
		defer it.Close()
		var buf []byte
		sizeHint := 0 // series in one query usually have similar sample counts
		for _, r := range refs {
			if err := ctx.Err(); err != nil {
				return err
			}
			prefix := dataPrefix(r.id)
			samples := make([]Sample, 0, sizeHint)
			for it.Seek(dataKey(r.id, mint)); it.ValidForPrefix(prefix); it.Next() {
				item := it.Item()
				t := dataKeyTime(item.Key())
				if t > maxt {
					break
				}
				var err error
				if buf, err = item.ValueCopy(buf[:0]); err != nil {
					return err
				}
				samples = append(samples, Sample{t, decodeValue(buf)})
			}
			sizeHint = len(samples)
			if len(samples) == 0 {
				continue
			}
			total += len(samples)
			if maxSamples > 0 && total > maxSamples {
				return fmt.Errorf("%w (limit %d)", ErrSampleLimit, maxSamples)
			}
			out = append(out, Series{Labels: r.lset, Samples: samples})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type seriesRef struct {
	id   uint64
	lset labels.Labels
}

// matchingSeries resolves matchers through the postings and returns series
// whose stored time range overlaps [mint, maxt], sorted by labels.
func (db *DB) matchingSeries(mint, maxt int64, ms []*labels.Matcher) []seriesRef {
	db.mu.RLock()
	defer db.mu.RUnlock()

	// Narrow with the most selective matcher that rejects the empty value;
	// such a matcher only accepts series that carry the label.
	var candidates []uint64
	narrowed := false
	for _, m := range ms {
		if m.MatchesEmpty() {
			continue
		}
		var ids []uint64
		for v, p := range db.postings[m.Name] {
			if m.Matches(v) {
				ids = union(ids, p)
			}
		}
		if !narrowed || len(ids) < len(candidates) {
			candidates, narrowed = ids, true
		}
	}
	if !narrowed {
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
		if last == math.MinInt64 || last < mint || first > maxt {
			continue
		}
		out = append(out, seriesRef{s.id, s.lset})
	}
	sort.Slice(out, func(i, j int) bool { return labels.Compare(out[i].lset, out[j].lset) < 0 })
	return out
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

// LabelNames returns the sorted label names used by series with data in
// [mint, maxt] that match ms.
func (db *DB) LabelNames(mint, maxt int64, ms ...*labels.Matcher) []string {
	set := map[string]struct{}{}
	for _, ls := range db.Series(mint, maxt, ms...) {
		for _, l := range ls {
			set[l.Name] = struct{}{}
		}
	}
	return sortedKeys(set)
}

// LabelValues returns the sorted values of label name across series with data
// in [mint, maxt] that match ms.
func (db *DB) LabelValues(name string, mint, maxt int64, ms ...*labels.Matcher) []string {
	set := map[string]struct{}{}
	for _, ls := range db.Series(mint, maxt, ms...) {
		if v := ls.Get(name); v != "" {
			set[v] = struct{}{}
		}
	}
	return sortedKeys(set)
}

// Stats describes the database.
type Stats struct {
	NumSeries          int
	MaxSeries          int
	MaxSeriesPerMetric int
	MinTime            int64 // ms; 0 when empty
	MaxTime            int64
	DiskBytes          int64
	SamplesAppended    uint64            // since start
	SamplesRejected    map[string]uint64 // since start, by reason
	Retention          time.Duration
}

// Stats returns current statistics.
func (db *DB) Stats() Stats {
	st := Stats{MaxSeries: db.opts.MaxSeries, MaxSeriesPerMetric: db.opts.MaxSeriesPerMetric, Retention: db.opts.Retention}
	minT, maxT := int64(math.MaxInt64), int64(math.MinInt64)
	db.mu.RLock()
	st.NumSeries = len(db.series)
	for _, s := range db.series {
		s.mu.Lock()
		if s.lastT != math.MinInt64 {
			minT = min(minT, s.firstT)
			maxT = max(maxT, s.lastT)
		}
		s.mu.Unlock()
	}
	db.mu.RUnlock()
	if maxT != math.MinInt64 {
		st.MinTime, st.MaxTime = minT, maxT
	}
	st.DiskBytes = db.diskUsage()
	db.counters.Lock()
	st.SamplesAppended = db.counters.appended
	st.SamplesRejected = make(map[string]uint64, len(db.counters.rejected))
	for k, v := range db.counters.rejected {
		st.SamplesRejected[k] = v
	}
	db.counters.Unlock()
	return st
}

// diskUsage returns the space used by the data directory.
func (db *DB) diskUsage() int64 {
	if db.opts.InMemory {
		lsm, vlog := db.kv.Size()
		return lsm + vlog
	}
	var n int64
	filepath.WalkDir(db.opts.Dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			n += allocated(fi)
		}
		return nil
	})
	return n
}

// Count is a name and the number of series it applies to.
type Count struct {
	Name  string
	Value int
}

// Cardinality summarizes the index the way Prometheus's /api/v1/status/tsdb
// does: series per metric, distinct values per label name, and series per
// label pair. Each list is sorted descending and truncated to limit.
type Cardinality struct {
	SeriesCountByMetricName     []Count
	LabelValueCountByLabelName  []Count
	SeriesCountByLabelValuePair []Count
}

// Cardinality returns index statistics.
func (db *DB) Cardinality(limit int) Cardinality {
	db.mu.RLock()
	defer db.mu.RUnlock()
	var c Cardinality
	for v, ids := range db.postings[labels.MetricName] {
		c.SeriesCountByMetricName = append(c.SeriesCountByMetricName, Count{v, len(ids)})
	}
	for name, vals := range db.postings {
		if name != labels.MetricName {
			c.LabelValueCountByLabelName = append(c.LabelValueCountByLabelName, Count{name, len(vals)})
		}
		for v, ids := range vals {
			c.SeriesCountByLabelValuePair = append(c.SeriesCountByLabelValuePair, Count{name + "=" + v, len(ids)})
		}
	}
	top := func(cs []Count) []Count {
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].Value != cs[j].Value {
				return cs[i].Value > cs[j].Value
			}
			return cs[i].Name < cs[j].Name
		})
		if len(cs) > limit {
			cs = cs[:limit]
		}
		return cs
	}
	c.SeriesCountByMetricName = top(c.SeriesCountByMetricName)
	c.LabelValueCountByLabelName = top(c.LabelValueCountByLabelName)
	c.SeriesCountByLabelValuePair = top(c.SeriesCountByLabelValuePair)
	return c
}

// ApplyRetention deletes samples older than the retention window, and series
// left with no samples. Deletion is batched, so its memory use is bounded.
//
// The bulk of the work, deleting expired samples, runs without blocking
// ingestion: appends only ever add samples newer than a series' last one, so
// they never touch the keys being deleted. Only removing dead series takes
// the commit lock, and only for the few series that went quiet.
func (db *DB) ApplyRetention() error {
	cutoff := db.opts.Now().Add(-db.opts.Retention).UnixMilli()

	db.mu.RLock()
	var expired []*memSeries
	for _, s := range db.series {
		s.mu.Lock()
		if s.firstT < cutoff {
			expired = append(expired, s)
		}
		s.mu.Unlock()
	}
	db.mu.RUnlock()
	if len(expired) == 0 {
		return nil
	}

	// Phase 1: delete expired samples, concurrently with appends.
	wb := db.kv.NewWriteBatch() // splits into multiple transactions as needed
	defer wb.Cancel()
	var empty []*memSeries
	err := db.kv.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		for _, s := range expired {
			opts.Prefix = dataPrefix(s.id)
			it := txn.NewIterator(opts)
			newFirst := int64(math.MaxInt64)
			for it.Rewind(); it.Valid(); it.Next() {
				t := dataKeyTime(it.Item().Key())
				if t >= cutoff {
					newFirst = t
					break
				}
				if err := wb.Delete(it.Item().KeyCopy(nil)); err != nil {
					it.Close()
					return err
				}
			}
			it.Close()
			if newFirst == math.MaxInt64 {
				empty = append(empty, s)
				continue
			}
			s.mu.Lock()
			s.firstT = newFirst
			s.mu.Unlock()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := wb.Flush(); err != nil {
		return err
	}

	// Phase 2: drop series that are still empty. A series may have received
	// samples since phase 1, so re-check under the commit lock.
	// Chunked so a mass expiry stays within Badger's transaction size limit.
	for len(empty) > 0 {
		n := min(len(empty), 1000)
		if err := db.dropEmptySeries(empty[:n]); err != nil {
			return err
		}
		empty = empty[n:]
	}

	if !db.opts.InMemory {
		for db.kv.RunValueLogGC(0.5) == nil {
		}
	}
	return nil
}

func (db *DB) dropEmptySeries(candidates []*memSeries) error {
	db.commitMu.Lock()
	defer db.commitMu.Unlock()
	var dead []*memSeries
	err := db.kv.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		for _, s := range candidates {
			opts.Prefix = dataPrefix(s.id)
			it := txn.NewIterator(opts)
			it.Rewind()
			var remaining int64 = math.MaxInt64
			if it.Valid() {
				remaining = dataKeyTime(it.Item().Key())
			}
			it.Close()
			if remaining != math.MaxInt64 {
				// Samples arrived after phase 1. Leave the series; any that
				// are older than the cutoff go on the next pass.
				s.mu.Lock()
				s.firstT = remaining
				s.mu.Unlock()
				continue
			}
			if err := txn.Delete(seriesKey(s.id)); err != nil {
				return err
			}
			dead = append(dead, s)
		}
		return nil
	})
	if err != nil {
		return err
	}
	db.mu.Lock()
	for _, s := range dead {
		db.unindexSeries(s)
	}
	db.mu.Unlock()
	return nil
}

// Key and value encodings.

func seriesKey(id uint64) []byte {
	k := make([]byte, 9)
	k[0] = prefixSeries
	binary.BigEndian.PutUint64(k[1:], id)
	return k
}

func dataPrefix(id uint64) []byte {
	k := make([]byte, 9)
	k[0] = prefixData
	binary.BigEndian.PutUint64(k[1:], id)
	return k
}

func dataKey(id uint64, t int64) []byte {
	k := make([]byte, 17)
	k[0] = prefixData
	binary.BigEndian.PutUint64(k[1:], id)
	binary.BigEndian.PutUint64(k[9:], uint64(t)^(1<<63))
	return k
}

func dataKeyTime(k []byte) int64 { return int64(binary.BigEndian.Uint64(k[9:]) ^ (1 << 63)) }

func encodeValue(v float64) []byte {
	return binary.BigEndian.AppendUint64(make([]byte, 0, 8), math.Float64bits(v))
}

func decodeValue(b []byte) float64 { return math.Float64frombits(binary.BigEndian.Uint64(b)) }

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

var errCorrupt = errors.New("corrupt label encoding")

func decodeLabels(b []byte) (labels.Labels, error) {
	next := func() (string, error) {
		n, k := binary.Uvarint(b)
		if k <= 0 || n > uint64(len(b)-k) {
			return "", errCorrupt
		}
		s := string(b[k : k+int(n)])
		b = b[k+int(n):]
		return s, nil
	}
	n, k := binary.Uvarint(b)
	if k <= 0 || n > labels.MaxLabels+1 {
		return nil, errCorrupt
	}
	b = b[k:]
	ls := make(labels.Labels, 0, n)
	for i := uint64(0); i < n; i++ {
		name, err := next()
		if err != nil {
			return nil, err
		}
		value, err := next()
		if err != nil {
			return nil, err
		}
		ls = append(ls, labels.Label{Name: name, Value: value})
	}
	if len(b) != 0 {
		return nil, errCorrupt
	}
	return ls, nil
}

func insertSorted(ids []uint64, id uint64) []uint64 {
	i := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	if i < len(ids) && ids[i] == id {
		return ids
	}
	ids = append(ids, 0)
	copy(ids[i+1:], ids[i:])
	ids[i] = id
	return ids
}

func removeSorted(ids []uint64, id uint64) []uint64 {
	i := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	if i < len(ids) && ids[i] == id {
		return append(ids[:i:i], ids[i+1:]...)
	}
	return ids
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

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
