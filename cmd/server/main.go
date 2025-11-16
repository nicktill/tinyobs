package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"tinyobs/pkg/compaction"
	"tinyobs/pkg/ingest"
	"tinyobs/pkg/storage/badger"

	"github.com/gorilla/mux"
)

const (
	// Server configuration
	serverReadTimeout     = 10 * time.Second
	serverWriteTimeout    = 10 * time.Second
	shutdownTimeout       = 30 * time.Second
	compactionInterval    = 1 * time.Hour
	maxStorageBytes       = 10 * 1024 * 1024 * 1024 // 10 GB default
)

// StorageUsage represents current storage usage stats
type StorageUsage struct {
	UsedBytes int64 `json:"used_bytes"`
	MaxBytes  int64 `json:"max_bytes"`
}

// calculateDirSize recursively calculates directory size in bytes
// Uses actual disk blocks allocated (like `du`) rather than logical file size
// to handle sparse files correctly (e.g., BadgerDB's .vlog files)
func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			// Get actual disk usage using blocks allocated
			stat, ok := info.Sys().(*syscall.Stat_t)
			if ok {
				// Blocks are 512 bytes on most Unix systems
				size += stat.Blocks * 512
			} else {
				// Fallback to logical size if syscall fails
				size += info.Size()
			}
		}
		return nil
	})
	return size, err
}

// handleHealth returns service health status
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"version": "1.0.0",
		"uptime":  time.Since(startTime).String(),
	})
}

var startTime = time.Now()

// handleStorageUsage returns current storage usage
func handleStorageUsage(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		usedBytes, err := calculateDirSize(dataDir)
		if err != nil {
			log.Printf("❌ Failed to calculate storage usage: %v", err)
			http.Error(w, "Failed to calculate storage", http.StatusInternalServerError)
			return
		}

		usage := StorageUsage{
			UsedBytes: usedBytes,
			MaxBytes:  maxStorageBytes,
		}

		log.Printf("📊 Storage usage: %d bytes (%.2f MB)", usedBytes, float64(usedBytes)/(1024*1024))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(usage)
	}
}

func main() {
	log.Println("🚀 Starting TinyObs Server...")

	// Ensure data directory exists
	dataDir := "./data/tinyobs"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("❌ Failed to create data directory: %v", err)
	}
	log.Printf("📁 Data directory: %s", dataDir)

	// Initialize storage
	log.Println("💾 Initializing BadgerDB storage with Snappy compression...")
	store, err := badger.New(badger.Config{
		Path: dataDir,
	})
	if err != nil {
		log.Fatalf("❌ Failed to initialize storage: %v", err)
	}
	defer store.Close()
	log.Println("✅ BadgerDB storage initialized successfully")

	// Create ingest handler
	handler := ingest.NewHandler(store)
	log.Println("📊 Ingest handler created with cardinality protection")

	// Create compactor
	compactor := compaction.New(store)
	log.Printf("⚙️  Compaction engine ready (runs every %v)", compactionInterval)

	// Start background compaction with cleanup tracking
	var wg sync.WaitGroup
	stopCompaction := make(chan bool)
	wg.Add(1)
	go runCompaction(compactor, stopCompaction, &wg)

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
	api.HandleFunc("/metrics/list", handler.HandleMetricsList).Methods("GET")
	api.HandleFunc("/stats", handler.HandleStats).Methods("GET")
	api.HandleFunc("/cardinality", handler.HandleCardinalityStats).Methods("GET")
	api.HandleFunc("/storage", handleStorageUsage(dataDir)).Methods("GET")
	api.HandleFunc("/health", handleHealth).Methods("GET")

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
		log.Println("📡 API endpoints:")
		log.Println("   POST /v1/ingest          - Ingest metrics")
		log.Println("   GET  /v1/query          - Query metrics")
		log.Println("   GET  /v1/query/range    - Range queries")
		log.Println("   GET  /v1/stats          - Storage statistics")
		log.Println("   GET  /metrics           - Prometheus endpoint")
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

	// Stop background tasks first
	log.Println("⏸️  Stopping background compaction...")
	close(stopCompaction)

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	log.Println("🔄 Gracefully shutting down server...")
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("❌ Server forced to shutdown: %v", err)
	}

	// Wait for background goroutines to finish
	log.Println("⏳ Waiting for background tasks to complete...")
	wg.Wait()

	log.Println("👋 TinyObs server exited cleanly")
}

// runCompaction runs the compaction job periodically
func runCompaction(compactor *compaction.Compactor, stop chan bool, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(compactionInterval)
	defer ticker.Stop()

	// Run once on startup (non-blocking, tracked by parent WaitGroup)
	go func() {
		log.Println("🔧 Running initial compaction (raw → 5m → 1h aggregates)...")
		ctx := context.Background()
		start := time.Now()
		if err := compactor.CompactAndCleanup(ctx); err != nil {
			log.Printf("❌ Initial compaction failed: %v", err)
		} else {
			log.Printf("✅ Initial compaction completed in %v", time.Since(start).Round(time.Millisecond))
		}
	}()

	for {
		select {
		case <-ticker.C:
			log.Println("⏰ Scheduled compaction started...")
			ctx := context.Background()
			start := time.Now()
			if err := compactor.CompactAndCleanup(ctx); err != nil {
				log.Printf("❌ Compaction failed: %v", err)
			} else {
				log.Printf("✅ Compaction completed in %v (data cleanup + downsampling)", time.Since(start).Round(time.Millisecond))
			}
		case <-stop:
			log.Println("🛑 Stopping compaction scheduler")
			return
		}
	}
}
