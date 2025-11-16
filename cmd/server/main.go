package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tinyobs/pkg/compaction"
	"tinyobs/pkg/ingest"
	"tinyobs/pkg/storage/badger"

	"github.com/gorilla/mux"
)

func main() {
	// Initialize storage
	log.Println("Initializing BadgerDB storage at ./data/tinyobs")
	store, err := badger.New(badger.Config{
		Path: "./data/tinyobs",
	})
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Create ingest handler
	handler := ingest.NewHandler(store)

	// Create compactor
	compactor := compaction.New(store)

	// Start background compaction (runs every hour)
	stopCompaction := make(chan bool)
	go runCompaction(compactor, stopCompaction)
	defer close(stopCompaction)

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
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
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

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}

// runCompaction runs the compaction job every hour
func runCompaction(compactor *compaction.Compactor, stop chan bool) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once on startup
	log.Println("Running initial compaction...")
	ctx := context.Background()
	if err := compactor.CompactAndCleanup(ctx); err != nil {
		log.Printf("Initial compaction failed: %v", err)
	}

	for {
		select {
		case <-ticker.C:
			log.Println("Running scheduled compaction...")
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
