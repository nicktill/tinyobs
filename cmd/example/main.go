package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk"
	"github.com/nicktill/tinyobs/pkg/sdk/httpx"
)

var (
	activeRequests int64
	startTime      time.Time
)

func main() {
	startTime = time.Now()

	// Initialize TinyObs client
	log.Println("🚀 Initializing TinyObs client...")
	client, err := sdk.New(sdk.ClientConfig{
		Service:    "example-app",
		APIKey:     "demo-key",
		Endpoint:   "http://localhost:8080/v1/ingest",
		FlushEvery: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("❌ Failed to create TinyObs client: %v", err)
	}

	// Start the client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("📊 Starting metrics collection...")
	if err := client.Start(ctx); err != nil {
		log.Fatalf("❌ Failed to start TinyObs client: %v", err)
	}
	defer client.Stop()

	// Create custom metrics (business logic)
	activeUsers := client.Gauge("active_users")
	errorCounter := client.Counter("errors_total")

	// Create HTTP server
	mux := http.NewServeMux()

	// Setup all handlers
	setupHandlers(mux, errorCounter)

	// Add TinyObs middleware - automatically tracks http_requests_total and http_request_duration_seconds
	handler := httpx.Middleware(client)(mux)

	// Start server
	server := &http.Server{
		Addr:    ":3000",
		Handler: handler,
	}

	// Channel to signal when server is ready
	serverReady := make(chan bool)

	go func() {
		log.Println("🌐 Starting example app on :3000")
		log.Println("📊 Visit http://localhost:3000 to see the app in action")
		log.Println("📊 Visit http://localhost:8080 to see the TinyObs dashboard")

		// Signal that we're starting (server takes a moment to bind)
		time.Sleep(100 * time.Millisecond)
		serverReady <- true

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Server failed to start: %v", err)
		}
	}()

	// Wait for server to be ready before starting traffic
	<-serverReady
	log.Println("✅ Server is ready, starting traffic simulator...")

	// Start traffic simulator
	go startTrafficSimulator(ctx, activeUsers)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down example app...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("❌ Server forced to shutdown: %v", err)
	}

	log.Println("👋 Example app exited")
}
