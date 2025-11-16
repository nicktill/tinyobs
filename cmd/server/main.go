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
	// Ensure data directory exists
	dataDir := "./data/tinyobs"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Initialize storage
	log.Println("Initializing BadgerDB storage at " + dataDir)
	store, err := badger.New(badger.Config{
		Path: dataDir,
	})
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Create ingest handler
	handler := ingest.NewHandler(store)

	// Create compactor
	compactor := compaction.New(store)

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
		log.Printf("Starting TinyObs server on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Stop background tasks first
	close(stopCompaction)

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Wait for background goroutines to finish
	log.Println("Waiting for background tasks to complete...")
	wg.Wait()

	log.Println("Server exited")
}

// runCompaction runs the compaction job periodically
func runCompaction(compactor *compaction.Compactor, stop chan bool, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(compactionInterval)
	defer ticker.Stop()

	// Run once on startup (non-blocking, tracked by parent WaitGroup)
	go func() {
		log.Println("Running initial compaction...")
		ctx := context.Background()
		if err := compactor.CompactAndCleanup(ctx); err != nil {
			log.Printf("Initial compaction failed: %v", err)
		}
	}()

	for {
		select {
		case <-ticker.C:
			log.Println("Running scheduled compaction...")
			ctx := context.Background()
			if err := compactor.CompactAndCleanup(ctx); err != nil {
				log.Printf("Compaction failed: %v", err)
			} else {
				log.Println("Compaction completed successfully")
			}
		case <-stop:
			log.Println("Stopping compaction scheduler")
			return
		}
	}
}
