package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tinyobs/pkg/sdk"
	"tinyobs/pkg/sdk/httpx"
)

func main() {
	// Initialize TinyObs client
	client, err := sdk.New(sdk.ClientConfig{
		Service:   "example-app",
		APIKey:    "demo-key",
		Endpoint:  "http://localhost:8080/v1/ingest",
		FlushEvery: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create TinyObs client: %v", err)
	}

	// Start the client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := client.Start(ctx); err != nil {
		log.Fatalf("Failed to start TinyObs client: %v", err)
	}
	defer client.Stop()

	// Create metrics
	requestCounter := client.Counter("http_requests_total")
	requestDuration := client.Histogram("http_request_duration_seconds")
	activeUsers := client.Gauge("active_users")
	errorCounter := client.Counter("errors_total")

	// Create HTTP server with middleware
	mux := http.NewServeMux()
	
	// Add TinyObs middleware
	handler := httpx.Middleware(client)(mux)

	// Example endpoints
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Simulate some work
		time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
		
		// Increment active users
		activeUsers.Inc()
		defer activeUsers.Dec()
		
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"message": "Hello from TinyObs!", "timestamp": "%s"}`, time.Now().Format(time.RFC3339))
	})

	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		// Simulate API call
		time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)
		
		// Randomly simulate errors
		if rand.Float32() < 0.1 { // 10% error rate
			errorCounter.Inc("type", "api_error", "endpoint", "/api/users")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"users": [{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}]}`)
	})

	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		// Simulate order processing
		time.Sleep(time.Duration(rand.Intn(300)) * time.Millisecond)
		
		// Track order metrics
		requestCounter.Inc("endpoint", "/api/orders", "method", r.Method)
		
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"orders": [{"id": 1, "total": 99.99}, {"id": 2, "total": 149.99}]}`)
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "healthy", "timestamp": "%s"}`, time.Now().Format(time.RFC3339))
	})

	// Start server
	server := &http.Server{
		Addr:    ":3001",
		Handler: handler,
	}

	go func() {
		log.Println("Starting example app on :3001")
		log.Println("Visit http://localhost:3001 to see the app in action")
		log.Println("Visit http://localhost:8080 to see the TinyObs dashboard")
		
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Simulate some background activity
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Simulate some background metrics
				requestCounter.Inc("type", "background_job")
				requestDuration.Observe(rand.Float64() * 0.5) // 0-500ms
			}
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down example app...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Example app exited")
}
