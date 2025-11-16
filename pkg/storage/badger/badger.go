package badger

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
	"tinyobs/pkg/sdk/metrics"
	"tinyobs/pkg/storage"
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
}

// New creates a BadgerDB storage backend
func New(cfg Config) (*Storage, error) {
	opts := badger.DefaultOptions(cfg.Path)

	if cfg.InMemory {
		opts = opts.WithInMemory(true)
	}

	// Optimize for time-series workload
	opts = opts.
		WithCompression(options.Snappy). // Compression for metrics
		WithNumVersionsToKeep(1)         // We don't need versioning

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger: %w", err)
	}

	return &Storage{db: db}, nil
}

// Write stores metrics in BadgerDB
func (s *Storage) Write(ctx context.Context, metrics []metrics.Metric) error {
	return s.db.Update(func(txn *badger.Txn) error {
		for _, m := range metrics {
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
}

// Query retrieves metrics matching the request
func (s *Storage) Query(ctx context.Context, req storage.QueryRequest) ([]metrics.Metric, error) {
	var results []metrics.Metric

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100

		it := txn.NewIterator(opts)
		defer it.Close()

		// Scan all keys (in production, would use prefix for efficiency)
		for it.Rewind(); it.Valid(); it.Next() {
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
		return nil
	})

	return results, err
}

// Delete removes metrics older than the given time
func (s *Storage) Delete(ctx context.Context, before time.Time) error {
	return s.db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // We only need keys

		it := txn.NewIterator(opts)
		defer it.Close()

		var keysToDelete [][]byte

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()

			// Extract timestamp from key
			_, ts := parseKey(item.Key())
			if ts.Before(before) {
				// Copy key (can't delete during iteration)
				key := item.KeyCopy(nil)
				keysToDelete = append(keysToDelete, key)
			}
		}

		// Delete collected keys
		for _, key := range keysToDelete {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}

		return nil
	})
}

// Close shuts down BadgerDB cleanly
func (s *Storage) Close() error {
	return s.db.Close()
}

// Stats returns storage statistics
func (s *Storage) Stats(ctx context.Context) (*storage.Stats, error) {
	stats := &storage.Stats{}

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		seriesMap := make(map[string]bool)
		var oldestTS, newestTS time.Time

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			stats.TotalMetrics++

			// Parse key to get series and timestamp
			seriesKey, ts := parseKey(item.Key())
			seriesMap[seriesKey] = true

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

	if err != nil {
		return nil, err
	}

	// Get DB size from LSM
	lsmSize, vlogSize := s.db.Size()
	stats.SizeBytes = uint64(lsmSize + vlogSize)

	return stats, nil
}

// makeKey creates a sortable key: series_hash + timestamp
// Format: [series_hash (8 bytes)][timestamp (8 bytes)]
func makeKey(name string, labels map[string]string, ts time.Time) []byte {
	// Simple hash for now (in production, use xxhash)
	seriesKey := seriesKeyString(name, labels)
	hash := simpleHash(seriesKey)

	key := make([]byte, 16)
	binary.BigEndian.PutUint64(key[0:8], hash)
	binary.BigEndian.PutUint64(key[8:16], uint64(ts.UnixNano()))

	return key
}

// parseKey extracts series key and timestamp from storage key
func parseKey(key []byte) (string, time.Time) {
	hash := binary.BigEndian.Uint64(key[0:8])
	tsNano := binary.BigEndian.Uint64(key[8:16])

	// We lose the original series string (only have hash)
	// In production, would store series metadata separately
	seriesKey := fmt.Sprintf("series_%d", hash)
	ts := time.Unix(0, int64(tsNano))

	return seriesKey, ts
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
		if m.Labels[k] != v {
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

// simpleHash is a placeholder hash function
// In production, use xxhash or similar
func simpleHash(s string) uint64 {
	var hash uint64 = 5381
	for i := 0; i < len(s); i++ {
		hash = ((hash << 5) + hash) + uint64(s[i])
	}
	return hash
}
