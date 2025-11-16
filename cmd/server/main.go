package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
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
)

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

	// API routes
	api := router.PathPrefix("/v1").Subrouter()
	api.HandleFunc("/ingest", handler.HandleIngest).Methods("POST")
	api.HandleFunc("/query", handler.HandleQuery).Methods("GET")
	api.HandleFunc("/query/range", handler.HandleRangeQuery).Methods("GET")
	api.HandleFunc("/metrics/list", handler.HandleMetricsList).Methods("GET")
	api.HandleFunc("/stats", handler.HandleStats).Methods("GET")
	api.HandleFunc("/cardinality", handler.HandleCardinalityStats).Methods("GET")

	// Prometheus-compatible metrics endpoint (standard /metrics path)
	router.HandleFunc("/metrics", handler.HandlePrometheusMetrics).Methods("GET")

	// Serve static files
	router.PathPrefix("/").Handler(http.FileServer(http.Dir("./web/")))

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
