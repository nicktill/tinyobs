package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/nicktill/tinyobs/pkg/compaction"
	"github.com/nicktill/tinyobs/pkg/ingest"
	"github.com/nicktill/tinyobs/pkg/server/monitor"
	"github.com/nicktill/tinyobs/pkg/storage"
	"github.com/nicktill/tinyobs/pkg/storage/badger"
)

const (
	compactionInterval = 1 * time.Hour
)

// RunCompaction runs the compaction job periodically.
func RunCompaction(compactor *compaction.Compactor, monitor *monitor.CompactionMonitor, stop chan bool, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(compactionInterval)
	defer ticker.Stop()

	// Helper function to run compaction with retry and exponential backoff
	runWithRetry := func(ctx context.Context, isInitial bool) {
		maxRetries := 3
		baseDelay := 30 * time.Second

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				delay := baseDelay * time.Duration(1<<(attempt-1)) // Exponential backoff: 30s, 60s, 120s
				log.Printf("Retrying compaction in %v (attempt %d/%d)...", delay, attempt+1, maxRetries+1)
				select {
				case <-time.After(delay):
				case <-stop:
					return
				}
			}

			start := time.Now()
			err := compactor.CompactAndCleanup(ctx)

			if err == nil {
				// Success!
				monitor.RecordSuccess()
				if isInitial {
					log.Printf("Initial compaction completed in %v", time.Since(start).Round(time.Millisecond))
				} else {
					log.Printf("Compaction completed in %v (data cleanup + downsampling)", time.Since(start).Round(time.Millisecond))
				}
				return
			}

			// Failure - record and log
			monitor.RecordFailure(err)
			log.Printf("Compaction failed (attempt %d/%d): %v", attempt+1, maxRetries+1, err)

			// Check if we should alert
			status := monitor.Status()
			if status.ConsecutiveErrors > 3 {
				log.Printf("ALERT: Compaction has been failing! Consecutive errors: %d", status.ConsecutiveErrors)
			}
		}

		log.Printf("Compaction failed after %d attempts, will retry on next schedule", maxRetries+1)
	}

	// Run once on startup (non-blocking)
	go func() {
		log.Println("Running initial compaction (raw -> 5m -> 1h aggregates)...")
		runWithRetry(context.Background(), true)
	}()

	for {
		select {
		case <-ticker.C:
			log.Println("Scheduled compaction started...")
			runWithRetry(context.Background(), false)
		case <-stop:
			log.Println("Stopping compaction scheduler")
			return
		}
	}
}

// BroadcastMetrics periodically fetches and broadcasts metrics to WebSocket clients.
// Uses exponential backoff on errors to prevent log spam during outages.
func BroadcastMetrics(ctx context.Context, store storage.Storage, hub *ingest.MetricsHub) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Exponential backoff state for error handling
	var consecutiveErrors int
	var lastErrorTime time.Time
	const maxBackoff = 5 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Skip querying if no clients connected - saves resources
			if !hub.HasClients() {
				continue
			}

			// Query recent metrics (last 1 minute) for live updates
			results, err := store.Query(ctx, storage.QueryRequest{
				Start: time.Now().Add(-1 * time.Minute),
				End:   time.Now(),
				Limit: 1000, // Limit to prevent overwhelming clients
			})
			if err != nil {
				consecutiveErrors++
				now := time.Now()

				// Exponential backoff: 1s, 2s, 4s, 8s, 16s, 32s, 64s, 128s, 256s (max 5m)
				// Prevents log spam during persistent errors or outages
				backoff := time.Duration(1<<uint(min(consecutiveErrors-1, 8))) * time.Second
				if backoff > maxBackoff {
					backoff = maxBackoff
				}

				// Only log if enough time has passed since last error
				if lastErrorTime.IsZero() || now.Sub(lastErrorTime) >= backoff {
					log.Printf("Failed to query metrics for broadcast (error #%d, backoff %v): %v",
						consecutiveErrors, backoff, err)
					lastErrorTime = now
				}
				continue
			}

			// Reset error count on success
			if consecutiveErrors > 0 {
				log.Printf("Metrics broadcast recovered after %d errors", consecutiveErrors)
				consecutiveErrors = 0
			}

			// Only broadcast if we have data
			if len(results) > 0 {
				// Broadcast metrics update to all connected WebSocket clients
				update := map[string]interface{}{
					"type":      "metrics_update",
					"timestamp": time.Now().Unix(),
					"metrics":   results,
					"count":     len(results),
				}

				if err := hub.Broadcast(update); err != nil {
					log.Printf("Failed to broadcast metrics: %v", err)
				}
			}
		}
	}
}

// RunBadgerGC runs BadgerDB garbage collection periodically to reclaim disk space.
// BadgerDB uses LSM trees which accumulate deleted data in value log.
// GC is essential to prevent unbounded disk growth.
func RunBadgerGC(store storage.Storage, stop chan bool, wg *sync.WaitGroup) {
	defer wg.Done()

	// GC runs every 10 minutes
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	// Type assert to get underlying BadgerDB
	badgerStore, ok := store.(*badger.Storage)
	if !ok {
		log.Println("Storage is not BadgerDB, skipping GC")
		return
	}

	log.Println("BadgerDB GC scheduler started (runs every 10m)")

	for {
		select {
		case <-ticker.C:
			// Run GC with 0.5 discard ratio (reclaim space if 50% of file is garbage)
			log.Println("Running BadgerDB garbage collection...")
			start := time.Now()

			// RunValueLogGC runs until no more garbage can be collected
			// We limit to 1 iteration per tick to avoid blocking
			err := badgerStore.RunGC(0.5)
			if err != nil {
				// Not an error if no GC was needed
				log.Printf("GC completed in %v (no rewrite needed)", time.Since(start).Round(time.Millisecond))
			} else {
				log.Printf("GC completed in %v (disk space reclaimed)", time.Since(start).Round(time.Millisecond))
			}
		case <-stop:
			log.Println("Stopping BadgerDB GC scheduler")
			return
		}
	}
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
