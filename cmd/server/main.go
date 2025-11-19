package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/nicktill/tinyobs/pkg/compaction"
	"github.com/nicktill/tinyobs/pkg/ingest"
	"github.com/nicktill/tinyobs/pkg/query"
	"github.com/nicktill/tinyobs/pkg/storage"
	"github.com/nicktill/tinyobs/pkg/storage/badger"
	"github.com/nicktill/tinyobs/pkg/tracing"

	"github.com/gorilla/mux"
)

const (
	// Server configuration
	serverReadTimeout  = 10 * time.Second
	serverWriteTimeout = 10 * time.Second
	shutdownTimeout    = 30 * time.Second
	compactionInterval = 1 * time.Hour

	// SAFETY: Conservative storage limit for self-hosted laptops
	// With compaction: ~50 MB after 30 days, ~70 MB after 1 year
	// This allows headroom for growth while preventing disk bloat
	defaultMaxStorageGB = 1 // 1 GB default (was 10 GB - way too high!)
)

// StorageUsage represents current storage usage stats
type StorageUsage struct {
	UsedBytes int64 `json:"used_bytes"`
	MaxBytes  int64 `json:"max_bytes"`
}

// StorageMonitor tracks storage usage with caching to avoid expensive filesystem calls
type StorageMonitor struct {
	dataDir       string
	maxBytes      int64
	cachedUsage   int64
	lastCheck     time.Time
	cacheDuration time.Duration
	mu            sync.Mutex // FIXED: Use single mutex instead of RWMutex to avoid race condition
}

// CompactionMonitor tracks compaction health and failures
type CompactionMonitor struct {
	mu                sync.RWMutex
	lastSuccess       time.Time
	lastAttempt       time.Time
	consecutiveErrors int
	lastError         string
}

// RecordSuccess records a successful compaction
func (cm *CompactionMonitor) RecordSuccess() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastSuccess = time.Now()
	cm.lastAttempt = time.Now()
	cm.consecutiveErrors = 0
	cm.lastError = ""
}

// RecordFailure records a failed compaction
func (cm *CompactionMonitor) RecordFailure(err error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastAttempt = time.Now()
	cm.consecutiveErrors++
	cm.lastError = err.Error()
}

// IsHealthy returns true if compaction is working properly
func (cm *CompactionMonitor) IsHealthy() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Unhealthy if:
	// 1. Never succeeded, OR
	// 2. Haven't succeeded in >1 hour, OR
	// 3. More than 3 consecutive failures
	if cm.lastSuccess.IsZero() {
		return false
	}
	if time.Since(cm.lastSuccess) > 1*time.Hour {
		return false
	}
	if cm.consecutiveErrors > 3 {
		return false
	}
	return true
}

// Status returns current compaction status for health checks
func (cm *CompactionMonitor) Status() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	status := map[string]interface{}{
		"healthy": cm.IsHealthy(),
	}

	if !cm.lastSuccess.IsZero() {
		status["last_success"] = cm.lastSuccess.Format(time.RFC3339)
		status["time_since_success"] = time.Since(cm.lastSuccess).String()
	}

	if !cm.lastAttempt.IsZero() {
		status["last_attempt"] = cm.lastAttempt.Format(time.RFC3339)
	}

	if cm.consecutiveErrors > 0 {
		status["consecutive_errors"] = cm.consecutiveErrors
		status["last_error"] = cm.lastError
	}

	return status
}

// NewStorageMonitor creates a storage monitor
func NewStorageMonitor(dataDir string, maxBytes int64) *StorageMonitor {
	return &StorageMonitor{
		dataDir:       dataDir,
		maxBytes:      maxBytes,
		cacheDuration: 10 * time.Second, // Cache for 10 seconds to avoid expensive disk scans
	}
}

// GetUsage returns current storage usage in bytes (cached)
// FIXED: Eliminated double-checked locking race condition by using single mutex
func (sm *StorageMonitor) GetUsage() (int64, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Return cached value if still fresh
	if time.Since(sm.lastCheck) < sm.cacheDuration {
		return sm.cachedUsage, nil
	}

	// Cache expired, recalculate
	usage, err := calculateDirSize(sm.dataDir)
	if err != nil {
		return 0, err
	}

	sm.cachedUsage = usage
	sm.lastCheck = time.Now()
	return usage, nil
}

// GetLimit returns the configured storage limit
func (sm *StorageMonitor) GetLimit() int64 {
	return sm.maxBytes
}

// calculateDirSize recursively calculates directory size in bytes
// Uses actual disk usage (not logical size) to handle sparse files correctly
func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			// Get actual disk usage, not logical file size
			actualSize, err := getActualFileSize(filePath, info)
			if err != nil {
				// Fallback to logical size if we can't get actual size
				size += info.Size()
			} else {
				size += actualSize
			}
		}
		return nil
	})
	return size, err
}

// getActualFileSize is implemented in platform-specific files:
// - filesize_unix.go (Linux/Mac): Uses syscall.Stat_t.Blocks
// - filesize_windows.go (Windows): Uses GetCompressedFileSizeW API

// handleHealth returns service health status
func handleHealth(compactionMonitor *CompactionMonitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Determine overall health status
		compactionHealthy := compactionMonitor.IsHealthy()
		overallStatus := "healthy"
		statusCode := http.StatusOK

		if !compactionHealthy {
			overallStatus = "degraded"
			statusCode = http.StatusServiceUnavailable
		}

		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     overallStatus,
			"version":    "1.0.0",
			"uptime":     time.Since(startTime).String(),
			"compaction": compactionMonitor.Status(),
		}); err != nil {
			log.Printf("❌ Failed to encode health response: %v", err)
		}
	}
}

var startTime = time.Now()

// getEnvInt64 gets an int64 from environment variable or returns default
func getEnvInt64(key string, defaultValue int64) int64 {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.ParseInt(val, 10, 64); err == nil {
			return parsed
		}
		log.Printf("⚠️  Invalid value for %s: %q, using default %d", key, val, defaultValue)
	}
	return defaultValue
}

// handleStorageUsage returns current storage usage
func handleStorageUsage(monitor *StorageMonitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		usedBytes, err := monitor.GetUsage()
		if err != nil {
			log.Printf("❌ Failed to calculate storage usage: %v", err)
			http.Error(w, "Failed to calculate storage", http.StatusInternalServerError)
			return
		}

		usage := StorageUsage{
			UsedBytes: usedBytes,
			MaxBytes:  monitor.GetLimit(),
		}

		log.Printf("📊 Storage usage: %d bytes (%.2f MB)", usedBytes, float64(usedBytes)/(1024*1024))

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(usage); err != nil {
			log.Printf("❌ Failed to encode storage usage response: %v", err)
		}
	}
}

func main() {
	log.Println("🚀 Starting TinyObs Server...")

	// Read configuration from environment variables
	// TINYOBS_MAX_STORAGE_GB: Maximum storage in GB (default: 1 GB for laptops)
	// TINYOBS_MAX_MEMORY_MB: Maximum BadgerDB memory in MB (default: 48 MB)
	maxStorageGB := getEnvInt64("TINYOBS_MAX_STORAGE_GB", defaultMaxStorageGB)
	maxMemoryMB := getEnvInt64("TINYOBS_MAX_MEMORY_MB", 48) // 48 MB default (16 MB memtable minimum)
	maxStorageBytesConfigured := maxStorageGB * 1024 * 1024 * 1024

	if maxMemoryMB > 0 {
		log.Printf("⚙️  Configuration: Storage limit = %.2f GB, Memory limit = %d MB",
			float64(maxStorageBytesConfigured)/(1024*1024*1024), maxMemoryMB)
	} else {
		log.Printf("⚙️  Configuration: Storage limit = %.2f GB, Memory limit = auto-detect",
			float64(maxStorageBytesConfigured)/(1024*1024*1024))
	}

	// Ensure data directory exists
	dataDir := "./data/tinyobs"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("❌ Failed to create data directory: %v", err)
	}
	log.Printf("📁 Data directory: %s", dataDir)

	// Initialize storage with memory limits
	log.Println("💾 Initializing BadgerDB storage with Snappy compression...")
	store, err := badger.New(badger.Config{
		Path:        dataDir,
		MaxMemoryMB: maxMemoryMB,
	})
	if err != nil {
		log.Fatalf("❌ Failed to initialize storage: %v", err)
	}
	defer store.Close()
	log.Println("✅ BadgerDB storage initialized successfully")

	// Create storage monitor for limit enforcement
	storageMonitor := NewStorageMonitor(dataDir, maxStorageBytesConfigured)
	log.Printf("💾 Storage limit enforcement enabled: %.2f GB max", float64(maxStorageBytesConfigured)/(1024*1024*1024))

	// Create ingest handler
	handler := ingest.NewHandler(store)
	handler.SetStorageChecker(storageMonitor)
	log.Println("📊 Ingest handler created with cardinality protection & storage limits")

	// Create query handler for TinyQuery (PromQL-compatible)
	queryHandler := query.NewHandler(store)
	log.Println("🔍 TinyQuery handler created (PromQL-compatible query engine)")

	// Create distributed tracing
	traceStorage := tracing.NewStorage()
	tracingHandler := tracing.NewHandler(traceStorage)
	log.Println("🔗 Distributed tracing enabled (stores up to 10k traces, 24h retention)")

	// Create WebSocket hub for real-time updates
	hub := ingest.NewMetricsHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		hub.Run(ctx)
	}()
	log.Println("📡 WebSocket hub started for real-time metrics streaming")

	// Start metrics broadcaster
	wg.Add(1)
	go func() {
		defer wg.Done()
		broadcastMetrics(ctx, store, hub)
	}()
	log.Println("📤 Metrics broadcaster started (updates every 5s)")

	// Create compactor with health monitoring
	compactor := compaction.New(store)
	compactionMonitor := &CompactionMonitor{}
	log.Printf("⚙️  Compaction engine ready (runs every %v)", compactionInterval)

	// Start background compaction with cleanup tracking and failure retry
	stopCompaction := make(chan bool)
	wg.Add(1)
	go runCompaction(compactor, compactionMonitor, stopCompaction, &wg)

	// Start BadgerDB garbage collection (reclaims disk space)
	stopGC := make(chan bool)
	wg.Add(1)
	go runBadgerGC(store, stopGC, &wg)

	// Create router
	router := mux.NewRouter()

	// CORS middleware for API access
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	// API routes
	api := router.PathPrefix("/v1").Subrouter()
	api.HandleFunc("/ingest", handler.HandleIngest).Methods("POST")
	api.HandleFunc("/query", handler.HandleQuery).Methods("GET")
	api.HandleFunc("/query/range", handler.HandleRangeQuery).Methods("GET")
	api.HandleFunc("/query/execute", queryHandler.HandleQueryExecute).Methods("POST")        // TinyQuery endpoint
	api.HandleFunc("/query/instant", queryHandler.HandleQueryInstant).Methods("GET", "POST") // Prometheus-compatible instant query
	api.HandleFunc("/metrics/list", handler.HandleMetricsList).Methods("GET")
	api.HandleFunc("/stats", handler.HandleStats).Methods("GET")
	api.HandleFunc("/cardinality", handler.HandleCardinalityStats).Methods("GET")
	api.HandleFunc("/storage", handleStorageUsage(storageMonitor)).Methods("GET")
	api.HandleFunc("/health", handleHealth(compactionMonitor)).Methods("GET")
	api.HandleFunc("/ws", handler.HandleWebSocket(hub)).Methods("GET")

	// Distributed tracing routes
	api.HandleFunc("/traces", tracingHandler.HandleQueryTraces).Methods("GET")
	api.HandleFunc("/traces/recent", tracingHandler.HandleRecentTraces).Methods("GET")
	api.HandleFunc("/traces/stats", tracingHandler.HandleTracingStats).Methods("GET")
	api.HandleFunc("/traces/ingest", tracingHandler.HandleIngestSpan).Methods("POST")
	api.HandleFunc("/trace", tracingHandler.HandleGetTrace).Methods("GET")

	// Prometheus-compatible metrics endpoint (standard /metrics path)
	router.HandleFunc("/metrics", handler.HandlePrometheusMetrics).Methods("GET")

	// Serve static files (strip prefix to prevent path traversal)
	fileServer := http.FileServer(http.Dir("./web/"))
	router.PathPrefix("/").Handler(http.StripPrefix("/", fileServer))

	// Create server
	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
	}

	// Start server in goroutine
	go func() {
		log.Println("🌐 Server starting on http://localhost:8080")
		log.Println("📊 Dashboard: http://localhost:8080/dashboard.html")
		log.Println("🔗 Tracing UI: http://localhost:8080/traces.html")
		log.Println("📡 API endpoints:")
		log.Println("   POST /v1/ingest          - Ingest metrics")
		log.Println("   GET  /v1/query          - Query metrics")
		log.Println("   GET  /v1/query/range    - Range queries")
		log.Println("   GET  /v1/stats          - Storage statistics")
		log.Println("   GET  /metrics           - Prometheus endpoint")
		log.Println("   GET  /v1/traces         - Query traces")
		log.Println("   GET  /v1/traces/recent  - Recent traces")
		log.Println("✅ Server ready to accept requests")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutdown signal received...")

	// CRITICAL: Cancel context FIRST to stop goroutines
	// Must be called before wg.Wait() or we get deadlock!
	log.Println("⏸️  Stopping background tasks...")
	cancel() // Stops hub.Run() and broadcastMetrics() goroutines
	close(stopCompaction)
	close(stopGC)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	log.Println("🔄 Gracefully shutting down server...")
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("⚠️  Server shutdown warning: %v", err)
	}

	// Wait for background goroutines to finish
	log.Println("⏳ Waiting for background tasks to complete...")
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Wait with timeout to prevent infinite hang
	select {
	case <-done:
		log.Println("✅ All background tasks stopped cleanly")
	case <-time.After(5 * time.Second):
		log.Println("⚠️  Some background tasks did not stop in time (forcing exit)")
	}

	log.Println("👋 TinyObs server exited cleanly")
}

// runCompaction runs the compaction job periodically
func runCompaction(compactor *compaction.Compactor, monitor *CompactionMonitor, stop chan bool, wg *sync.WaitGroup) {
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
				log.Printf("⏳ Retrying compaction in %v (attempt %d/%d)...", delay, attempt+1, maxRetries+1)
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
					log.Printf("✅ Initial compaction completed in %v", time.Since(start).Round(time.Millisecond))
				} else {
					log.Printf("✅ Compaction completed in %v (data cleanup + downsampling)", time.Since(start).Round(time.Millisecond))
				}
				return
			}

			// Failure - record and log
			monitor.RecordFailure(err)
			log.Printf("❌ Compaction failed (attempt %d/%d): %v", attempt+1, maxRetries+1, err)

			// Check if we should alert
			if monitor.consecutiveErrors > 3 || time.Since(monitor.lastSuccess) > 1*time.Hour {
				log.Printf("🚨 ALERT: Compaction has been failing! Consecutive errors: %d, Time since success: %v",
					monitor.consecutiveErrors, time.Since(monitor.lastSuccess))
			}
		}

		log.Printf("❌ Compaction failed after %d attempts, will retry on next schedule", maxRetries+1)
	}

	// Run once on startup (non-blocking)
	go func() {
		log.Println("🔧 Running initial compaction (raw → 5m → 1h aggregates)...")
		runWithRetry(context.Background(), true)
	}()

	for {
		select {
		case <-ticker.C:
			log.Println("⏰ Scheduled compaction started...")
			runWithRetry(context.Background(), false)
		case <-stop:
			log.Println("🛑 Stopping compaction scheduler")
			return
		}
	}
}

// broadcastMetrics periodically fetches and broadcasts metrics to WebSocket clients
// CRITICAL: Uses exponential backoff on errors to prevent log spam during outages
func broadcastMetrics(ctx context.Context, store storage.Storage, hub *ingest.MetricsHub) {
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
					log.Printf("❌ Failed to query metrics for broadcast (error #%d, backoff %v): %v",
						consecutiveErrors, backoff, err)
					lastErrorTime = now
				}
				continue
			}

			// Reset error count on success
			if consecutiveErrors > 0 {
				log.Printf("✅ Metrics broadcast recovered after %d errors", consecutiveErrors)
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
					log.Printf("❌ Failed to broadcast metrics: %v", err)
				}
			}
		}
	}
}

// min returns the minimum of two integers (Go 1.20 doesn't have builtin min for int)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// runBadgerGC runs BadgerDB garbage collection periodically to reclaim disk space
// SAFETY: BadgerDB uses LSM trees which accumulate deleted data in value log
// GC is essential to prevent unbounded disk growth
func runBadgerGC(store storage.Storage, stop chan bool, wg *sync.WaitGroup) {
	defer wg.Done()

	// GC runs every 10 minutes
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	// Type assert to get underlying BadgerDB
	badgerStore, ok := store.(*badger.Storage)
	if !ok {
		log.Println("⚠️  Storage is not BadgerDB, skipping GC")
		return
	}

	log.Println("🗑️  BadgerDB GC scheduler started (runs every 10m)")

	for {
		select {
		case <-ticker.C:
			// Run GC with 0.5 discard ratio (reclaim space if 50% of file is garbage)
			log.Println("🗑️  Running BadgerDB garbage collection...")
			start := time.Now()

			// RunValueLogGC runs until no more garbage can be collected
			// We limit to 1 iteration per tick to avoid blocking
			err := badgerStore.RunGC(0.5)
			if err != nil {
				// Not an error if no GC was needed
				log.Printf("🗑️  GC completed in %v (no rewrite needed)", time.Since(start).Round(time.Millisecond))
			} else {
				log.Printf("✅ GC completed in %v (disk space reclaimed)", time.Since(start).Round(time.Millisecond))
			}
		case <-stop:
			log.Println("🛑 Stopping BadgerDB GC scheduler")
			return
		}
	}
}
