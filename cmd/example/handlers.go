package main

import (
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// setupHandlers configures all HTTP handlers
func setupHandlers(mux *http.ServeMux, errorCounter metrics.CounterInterface) {
	// Stats page with live dashboard
	mux.HandleFunc("/", serveStatsPage)

	// Example endpoints with predictable latencies for demo
	// NOTE: These endpoints return MOCK data (fake JSON), but the metrics are REAL!
	// - httpx.Middleware automatically tracks http_requests_total and http_request_duration_seconds
	// - errorCounter tracks business logic errors
	// - activeRequests tracks in-flight requests (for UI stats)
	mux.HandleFunc("/api/users", handleUsers(errorCounter))
	mux.HandleFunc("/api/orders", handleOrders())
	mux.HandleFunc("/api/products", handleProducts())

	// Health check endpoint
	mux.HandleFunc("/health", handleHealth())

	// Stats API - queries TinyObs for real metrics from httpx.Middleware
	mux.HandleFunc("/api/stats", handleStats())
}

// handleUsers handles /api/users endpoint
func handleUsers(errorCounter metrics.CounterInterface) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&activeRequests, 1)
		defer atomic.AddInt64(&activeRequests, -1)

		// Consistent latency: 50-100ms (simulated work)
		latency := time.Duration(50+rand.Intn(50)) * time.Millisecond
		time.Sleep(latency)

		// Very rare errors (2%) for demo clarity
		// Note: httpx.Middleware automatically tracks http_requests_total{status="500"}
		// so we can query TinyObs for error counts - no manual tracking needed!
		if rand.Float32() < 0.02 {
			errorCounter.Inc("type", "api_error", "endpoint", "/api/users")
			log.Printf("⚠️  ERROR: /api/users request failed (latency: %v)", latency)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		log.Printf("✅ /api/users - 200 OK (latency: %v)", latency)

		// Mock response data
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"users": [{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}]}`))
	}
}

// handleOrders handles /api/orders endpoint
func handleOrders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&activeRequests, 1)
		defer atomic.AddInt64(&activeRequests, -1)

		// Consistent latency: 80-120ms (simulated work)
		latency := time.Duration(80+rand.Intn(40)) * time.Millisecond
		time.Sleep(latency)

		log.Printf("✅ /api/orders - 200 OK (latency: %v)", latency)

		// Mock response data
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"orders": [{"id": 1, "total": 99.99}, {"id": 2, "total": 149.99}]}`))
	}
}

// handleProducts handles /api/products endpoint
func handleProducts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&activeRequests, 1)
		defer atomic.AddInt64(&activeRequests, -1)

		// Consistent latency: 30-60ms (fastest endpoint, simulated work)
		latency := time.Duration(30+rand.Intn(30)) * time.Millisecond
		time.Sleep(latency)

		log.Printf("✅ /api/products - 200 OK (latency: %v)", latency)

		// Mock response data
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"products": [{"id": 1, "name": "Widget"}, {"id": 2, "name": "Gadget"}]}`))
	}
}

// handleHealth handles /health endpoint
func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "healthy", "uptime": "` + time.Since(startTime).Round(time.Second).String() + `", "timestamp": "` + time.Now().Format(time.RFC3339) + `"}`))
	}
}
