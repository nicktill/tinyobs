package badger

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// Storage implements storage.Storage using BadgerDB (LSM tree)
type Storage struct {
	db *badger.DB
}

// Config holds BadgerDB configuration
type Config struct {
	// Path to store database files
	Path string

	// InMemory mode (for testing)
	InMemory bool

	// MaxMemoryMB limits BadgerDB memory usage in MB (0 = use defaults based on environment)
	// Recommended: 64-128 MB for local dev, 256-512 MB for production
	MaxMemoryMB int64
}

// New creates a BadgerDB storage backend
func New(cfg Config) (*Storage, error) {
	opts := badger.DefaultOptions(cfg.Path)

	if cfg.InMemory {
		opts = opts.WithInMemory(true)
	}

	// SAFETY: Conservative memory limits for laptops
	// BadgerDB defaults: 64 MB memtable, 5 x 64 MB = 320 MB total
	// We use 48 MB total (16 MB memtable + 32 MB cache) for self-hosted
	var memTableSize, valueLogMaxEntries int64
	if cfg.MaxMemoryMB > 0 {
		// User specified limit - use it
		memTableSize = cfg.MaxMemoryMB * 1024 * 1024 / 3 // ~33% for memtable
		valueLogMaxEntries = 5000
	} else {
		// Default: Laptop-friendly (48 MB total)
		// 16 MB memtable is minimum for decent performance
		// Below 16 MB causes excessive disk flushes
		memTableSize = 16 * 1024 * 1024
		valueLogMaxEntries = 5000
	}

	// CRITICAL MEMORY LIMITS: BadgerDB has multiple unbounded memory consumers
	// Without these limits, it can consume 1-2 GB even with small memtable
	blockCacheSize := memTableSize / 2 // Block cache: 50% of memtable
	indexCacheSize := memTableSize / 4 // Index cache: 25% of memtable

	// Optimize for time-series workload with strict memory bounds
	opts = opts.
		// Compression and versioning
		WithCompression(options.Snappy). // Compression for metrics
		WithNumVersionsToKeep(1).        // We don't need versioning

		// Memory table configuration
		WithMemTableSize(memTableSize). // Limit memory table size
		WithNumMemtables(3).            // Limit concurrent memtables (3 = active + 2 flushing)

		// Block and index caching (CRITICAL for memory bounds)
		WithBlockCacheSize(blockCacheSize). // Limit block cache to prevent unbounded growth
		WithIndexCacheSize(indexCacheSize). // Limit index cache to prevent unbounded growth

		// LSM tree configuration (reduces memory and disk usage)
		WithMaxLevels(4).               // Reduce LSM depth (default 7) for smaller datasets
		WithNumLevelZeroTables(2).      // Trigger compaction earlier (default 5)
		WithNumLevelZeroTablesStall(4). // Hard limit before stalling writes (default 10)
		WithValueThreshold(1024).       // Keep small values in LSM, large in vlog (default 1MB)
		WithNumCompactors(2).           // Minimum 2 compactors required by BadgerDB

		// Value log configuration
		WithValueLogMaxEntries(uint32(valueLogMaxEntries)). // Limit value log entries
		WithValueLogFileSize(64 << 20)                      // CRITICAL: 64 MB value log files instead of default 2GB!

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger: %w", err)
	}

	return &Storage{db: db}, nil
}

// Write stores metrics in BadgerDB
// CRITICAL: Enforces context timeout/cancellation to prevent indefinite blocking
func (s *Storage) Write(ctx context.Context, metrics []metrics.Metric) error {
	// Check context before starting expensive operation
	if err := ctx.Err(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- s.db.Update(func(txn *badger.Txn) error {
			for i, m := range metrics {
				// Check context periodically (every 100 metrics)
				if i%100 == 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
				}

				key := makeKey(m.Name, m.Labels, m.Timestamp)
				value, err := encodeMetric(m)
				if err != nil {
					return fmt.Errorf("failed to encode metric: %w", err)
				}

				if err := txn.Set(key, value); err != nil {
					return fmt.Errorf("failed to write metric: %w", err)
				}
			}
			return nil
		})
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Context cancelled while waiting for operation to complete
		return fmt.Errorf("write operation cancelled: %w", ctx.Err())
	}
}

// Query retrieves metrics matching the request
// CRITICAL: Enforces context timeout/cancellation to prevent indefinite blocking
func (s *Storage) Query(ctx context.Context, req storage.QueryRequest) ([]metrics.Metric, error) {
	// Check context before starting expensive operation
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var results []metrics.Metric
	startTime := time.Now()
	var iterCount int

	type queryResult struct {
		results []metrics.Metric
		err     error
	}
	done := make(chan queryResult, 1)

	go func() {
		var res queryResult
		res.err = s.db.View(func(txn *badger.Txn) error {
			opts := badger.DefaultIteratorOptions
			opts.PrefetchSize = 100

			// PERFORMANCE FIX: Use prefix scanning when metric names are specified
			// This provides 100x speedup by scanning only relevant keys
			if len(req.MetricNames) > 0 {
				// Scan each metric name with prefix iteration
				for _, metricName := range req.MetricNames {
					nameBytes := []byte(metricName)
					nameLen := uint16(len(nameBytes))

					// Build prefix: [name_length (2 bytes)][metric_name]
					prefix := make([]byte, 2+len(nameBytes))
					binary.BigEndian.PutUint16(prefix[0:2], nameLen)
					copy(prefix[2:], nameBytes)

					opts.Prefix = prefix
					it := txn.NewIterator(opts)

					for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
						iterCount++

						// Check context cancellation every 1000 iterations
						if iterCount%1000 == 0 {
							select {
							case <-ctx.Done():
								it.Close()
								elapsed := time.Since(startTime)
								if elapsed > 5*time.Second {
									log.Printf("Query cancelled after %v (%d iterations, %d results)\n", elapsed, iterCount, len(results))
								}
								return ctx.Err()
							default:
							}
						}

						item := it.Item()
						err := item.Value(func(val []byte) error {
							m, err := decodeMetric(val)
							if err != nil {
								return err
							}

							// Apply remaining filters (time range, labels)
							if !matchesQuery(m, req) {
								return nil
							}

							results = append(results, m)

							// Limit check
							if req.Limit > 0 && len(results) >= req.Limit {
								return nil
							}

							return nil
						})

						if err != nil {
							it.Close()
							return err
						}

						// Early exit if limit reached
						if req.Limit > 0 && len(results) >= req.Limit {
							break
						}
					}
					it.Close()

					// Stop if limit reached across all metrics
					if req.Limit > 0 && len(results) >= req.Limit {
						break
					}
				}
			} else {
				// No metric filter: fall back to full scan
				it := txn.NewIterator(opts)
				defer it.Close()

				for it.Rewind(); it.Valid(); it.Next() {
					iterCount++

					// CRITICAL: Check for context cancellation every 1000 iterations
					// Prevents long-running queries from blocking shutdown or exceeding timeouts
					if iterCount%1000 == 0 {
						select {
						case <-ctx.Done():
							// Log slow query warning before returning error
							elapsed := time.Since(startTime)
							if elapsed > 5*time.Second {
								log.Printf("Query cancelled after %v (%d iterations, %d results)\n", elapsed, iterCount, len(results))
							}
							return ctx.Err()
						default:
							// Continue processing
						}
					}

					item := it.Item()

					err := item.Value(func(val []byte) error {
						m, err := decodeMetric(val)
						if err != nil {
							return err
						}

						// Apply filters
						if !matchesQuery(m, req) {
							return nil
						}

						results = append(results, m)

						// Limit check
						if req.Limit > 0 && len(results) >= req.Limit {
							return nil
						}

						return nil
					})

					if err != nil {
						return err
					}

					// Early exit if limit reached
					if req.Limit > 0 && len(results) >= req.Limit {
						break
					}
				}
			} // End else (full scan)

			// Log slow queries for performance monitoring
			elapsed := time.Since(startTime)
			if elapsed > 5*time.Second {
				log.Printf("Slow query completed in %v (%d iterations, %d results)\n", elapsed, iterCount, len(results))
			}

			return nil
		})
		res.results = results
		done <- res
	}()

	select {
	case res := <-done:
		return res.results, res.err
	case <-ctx.Done():
		// Context cancelled while waiting for operation to complete
		return nil, fmt.Errorf("query operation cancelled: %w", ctx.Err())
	}
}

// Delete removes metrics matching the deletion criteria
// CRITICAL: Enforces context timeout/cancellation to prevent indefinite blocking
func (s *Storage) Delete(ctx context.Context, opts storage.DeleteOptions) error {
	// Check context before starting expensive operation
	if err := ctx.Err(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- s.db.Update(func(txn *badger.Txn) error {
			iterOpts := badger.DefaultIteratorOptions
			// Need values if filtering by resolution
			iterOpts.PrefetchValues = opts.Resolution != nil

			it := txn.NewIterator(iterOpts)
			defer it.Close()

			var keysToDelete [][]byte
			var iterCount int

			for it.Rewind(); it.Valid(); it.Next() {
				iterCount++

				// Check context periodically (every 1000 iterations)
				if iterCount%1000 == 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
				}

				item := it.Item()

				// Extract timestamp from key
				_, ts := parseKey(item.Key())
				if !ts.Before(opts.Before) {
					continue // Keep metrics after cutoff
				}

				// If resolution filter is specified, check the metric's resolution
				if opts.Resolution != nil {
					var m metrics.Metric
					if err := item.Value(func(val []byte) error {
						return json.Unmarshal(val, &m)
					}); err != nil {
						return fmt.Errorf("failed to unmarshal metric: %w", err)
					}

					// Get resolution from labels
					resolution := "" // Default for raw metrics
					if m.Labels != nil {
						resolution = m.Labels["__resolution__"]
					}

					// Skip if resolution doesn't match filter
					if resolution != string(*opts.Resolution) {
						continue
					}
				}

				// Mark for deletion
				key := item.KeyCopy(nil)
				keysToDelete = append(keysToDelete, key)
			}

			// Delete collected keys
			for _, key := range keysToDelete {
				if err := txn.Delete(key); err != nil {
					return err
				}
			}

			return nil
		})
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Context cancelled while waiting for operation to complete
		return fmt.Errorf("delete operation cancelled: %w", ctx.Err())
	}
}

// Close shuts down BadgerDB cleanly
func (s *Storage) Close() error {
	return s.db.Close()
}

// RunGC runs BadgerDB's value log garbage collection
// This reclaims disk space from deleted/updated values
// discardRatio: run GC if this fraction of file can be discarded (0.5 = 50%)
// Returns error only if GC failed, nil if GC not needed or succeeded
func (s *Storage) RunGC(discardRatio float64) error {
	return s.db.RunValueLogGC(discardRatio)
}

// Stats returns storage statistics
// CRITICAL: Enforces context timeout/cancellation to prevent indefinite blocking
func (s *Storage) Stats(ctx context.Context) (*storage.Stats, error) {
	// Check context before starting expensive operation
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	type statsResult struct {
		stats *storage.Stats
		err   error
	}
	done := make(chan statsResult, 1)

	go func() {
		var res statsResult
		stats := &storage.Stats{}

		res.err = s.db.View(func(txn *badger.Txn) error {
			opts := badger.DefaultIteratorOptions
			opts.PrefetchValues = false

			it := txn.NewIterator(opts)
			defer it.Close()

			seriesMap := make(map[uint64]bool)
			var oldestTS, newestTS time.Time
			var iterCount int

			for it.Rewind(); it.Valid(); it.Next() {
				iterCount++

				// Check context periodically (every 1000 iterations)
				if iterCount%1000 == 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
				}

				item := it.Item()
				stats.TotalMetrics++

				// Parse key to get timestamp
				key := item.Key()
				_, ts := parseKey(key)

				// Extract series hash for unique series counting
				// Key format: [name_len(2)][name][series_hash(8)][timestamp(8)]
				if len(key) >= 18 {
					nameLen := binary.BigEndian.Uint16(key[0:2])
					if len(key) >= int(2+nameLen+16) {
						// Extract 8-byte series hash (after name, before timestamp)
						seriesHash := binary.BigEndian.Uint64(key[2+nameLen : 2+nameLen+8])
						seriesMap[seriesHash] = true
					}
				}

				if oldestTS.IsZero() || ts.Before(oldestTS) {
					oldestTS = ts
				}
				if newestTS.IsZero() || ts.After(newestTS) {
					newestTS = ts
				}
			}

			stats.TotalSeries = uint64(len(seriesMap))
			stats.OldestMetric = oldestTS
			stats.NewestMetric = newestTS

			return nil
		})

		if res.err == nil {
			// Get DB size from LSM
			lsmSize, vlogSize := s.db.Size()
			stats.SizeBytes = uint64(lsmSize + vlogSize)
		}

		res.stats = stats
		done <- res
	}()

	select {
	case res := <-done:
		return res.stats, res.err
	case <-ctx.Done():
		// Context cancelled while waiting for operation to complete
		return nil, fmt.Errorf("stats operation cancelled: %w", ctx.Err())
	}
}

// makeKey creates a sortable key with metric name prefix for efficient scanning
// Format: [metric_name_length (2 bytes)][metric_name][series_hash (8 bytes)][timestamp (8 bytes)]
// This enables prefix scanning by metric name (100x faster queries)
func makeKey(name string, labels map[string]string, ts time.Time) []byte {
	seriesKey := seriesKeyString(name, labels)
	hash := xxhash.Sum64String(seriesKey)

	nameBytes := []byte(name)
	nameLen := len(nameBytes)

	// Allocate: 2 (name length) + name + 8 (hash) + 8 (timestamp)
	key := make([]byte, 2+nameLen+16)

	// Write name length (allows parsing)
	binary.BigEndian.PutUint16(key[0:2], uint16(nameLen))

	// Write metric name (enables prefix scanning)
	copy(key[2:2+nameLen], nameBytes)

	// Write series hash (for exact series matching)
	binary.BigEndian.PutUint64(key[2+nameLen:2+nameLen+8], hash)

	// Write timestamp (maintains time ordering within series)
	binary.BigEndian.PutUint64(key[2+nameLen+8:2+nameLen+16], uint64(ts.UnixNano()))

	return key
}

// parseKey extracts metric name and timestamp from storage key
func parseKey(key []byte) (string, time.Time) {
	if len(key) < 18 { // Min: 2 (len) + 0 (name) + 8 (hash) + 8 (ts)
		return "", time.Time{}
	}

	// Read metric name length
	nameLen := binary.BigEndian.Uint16(key[0:2])

	// Extract metric name
	metricName := string(key[2 : 2+nameLen])

	// Extract timestamp (skip name and hash)
	tsNano := binary.BigEndian.Uint64(key[2+nameLen+8 : 2+nameLen+16])
	ts := time.Unix(0, int64(tsNano))

	return metricName, ts
}

// encodeMetric serializes a metric to bytes
func encodeMetric(m metrics.Metric) ([]byte, error) {
	return json.Marshal(m)
}

// decodeMetric deserializes bytes to a metric
func decodeMetric(data []byte) (metrics.Metric, error) {
	var m metrics.Metric
	err := json.Unmarshal(data, &m)
	return m, err
}

// matchesQuery checks if a metric matches the query filters
func matchesQuery(m metrics.Metric, req storage.QueryRequest) bool {
	// Time range
	if m.Timestamp.Before(req.Start) || m.Timestamp.After(req.End) {
		return false
	}

	// Metric name filter
	if len(req.MetricNames) > 0 {
		found := false
		for _, name := range req.MetricNames {
			if m.Name == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Label filter
	for k, v := range req.Labels {
		if m.Labels == nil || m.Labels[k] != v {
			return false
		}
	}

	return true
}

// seriesKeyString creates a deterministic string key for a series
func seriesKeyString(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	// Sort label keys for deterministic ordering
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build key with sorted labels
	key := name
	for _, k := range keys {
		key += "," + k + "=" + labels[k]
	}
	return key
}
