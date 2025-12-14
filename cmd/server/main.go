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

	"github.com/nicktill/tinyobs/pkg/server"
	"github.com/nicktill/tinyobs/pkg/server/monitor"

	"github.com/gorilla/mux"
)

const (
	serverReadTimeout  = 10 * time.Second
	serverWriteTimeout = 10 * time.Second
	shutdownTimeout    = 30 * time.Second
)

func main() {
	log.Println("Starting TinyObs Server...")

	// Load configuration
	cfg := server.LoadConfig()
	maxStorageBytes := cfg.MaxStorageGB * 1024 * 1024 * 1024

	if cfg.MaxMemoryMB > 0 {
		log.Printf("Configuration: Storage limit = %.2f GB, Memory limit = %d MB",
			float64(maxStorageBytes)/(1024*1024*1024), cfg.MaxMemoryMB)
	} else {
		log.Printf("Configuration: Storage limit = %.2f GB, Memory limit = auto-detect",
			float64(maxStorageBytes)/(1024*1024*1024))
	}
	log.Printf("Data directory: %s", cfg.DataDir)

	// Initialize storage
	store, err := server.InitializeStorage(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Create storage monitor for limit enforcement
	storageMonitor := monitor.NewStorageMonitor(cfg.DataDir, maxStorageBytes)
	log.Printf("Storage limit enforcement enabled: %.2f GB max", float64(maxStorageBytes)/(1024*1024*1024))

	// Initialize handlers
	ingestHandler, queryHandler, exportHandler, tracingHandler, hub := server.InitializeHandlers(store, storageMonitor)

	// Initialize compactor
	compactor, compactionMonitor := server.InitializeCompactor(store)

	// Create router
	router := mux.NewRouter()
	server.SetupRoutes(router, ingestHandler, queryHandler, exportHandler, tracingHandler, storageMonitor, compactionMonitor, hub, cfg.Port)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
	}

	// Start background tasks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// WebSocket hub
	wg.Add(1)
	go func() {
		defer wg.Done()
		hub.Run(ctx)
	}()
	log.Println("WebSocket hub started for real-time metrics streaming")

	// Metrics broadcaster
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.BroadcastMetrics(ctx, store, hub)
	}()
	log.Println("Metrics broadcaster started (updates every 5s)")

	// Compaction
	stopCompaction := make(chan bool)
	wg.Add(1)
	go server.RunCompaction(compactor, compactionMonitor, stopCompaction, &wg)

	// BadgerDB GC
	stopGC := make(chan bool)
	wg.Add(1)
	go server.RunBadgerGC(store, stopGC, &wg)

	// Start server in goroutine
	go func() {
		log.Printf("Server starting on http://localhost:%s", cfg.Port)
		log.Printf("Dashboard: http://localhost:%s/dashboard.html", cfg.Port)
		log.Printf("Tracing UI: http://localhost:%s/traces.html", cfg.Port)
		log.Println("API endpoints:")
		log.Println("   POST /v1/ingest          - Ingest metrics")
		log.Println("   GET  /v1/query          - Query metrics")
		log.Println("   GET  /v1/query/range    - Range queries")
		log.Println("   GET  /v1/stats          - Storage statistics")
		log.Println("   GET  /v1/export         - Export metrics (JSON/CSV)")
		log.Println("   POST /v1/import         - Import metrics from backup")
		log.Println("   GET  /metrics           - Prometheus endpoint")
		log.Println("   GET  /v1/traces         - Query traces")
		log.Println("   GET  /v1/traces/recent  - Recent traces")
		log.Println("Server ready to accept requests")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutdown signal received...")

	// Cancel context to stop goroutines
	log.Println("Stopping background tasks...")
	cancel()
	close(stopCompaction)
	close(stopGC)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	log.Println("Gracefully shutting down server...")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown warning: %v", err)
	}

	// Wait for background goroutines to finish
	log.Println("Waiting for background tasks to complete...")
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Wait with timeout to prevent infinite hang
	select {
	case <-done:
		log.Println("All background tasks stopped cleanly")
	case <-time.After(5 * time.Second):
		log.Println("Some background tasks did not stop in time (forcing exit)")
	}

	log.Println("TinyObs server exited cleanly")
}
