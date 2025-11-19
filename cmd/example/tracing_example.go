package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/tracing"
)

// Example demonstrating distributed tracing with TinyObs
// This creates a simple multi-service setup where:
// - API service receives requests
// - Database service handles queries
// - Cache service handles lookups
//
// Run this alongside the TinyObs server and view traces at:
// http://localhost:8080/traces.html

func runTracingExample() {
	// Create trace storage and tracer
	storage := tracing.NewStorage()

	// Create tracers for different services
	apiTracer := tracing.NewTracer("api-service", storage)
	dbTracer := tracing.NewTracer("database-service", storage)
	cacheTracer := tracing.NewTracer("cache-service", storage)

	log.Println("🔗 Starting distributed tracing example...")
	log.Println("📊 View traces at http://localhost:8080/traces.html")

	// Create HTTP server with tracing middleware
	mux := http.NewServeMux()

	// API endpoints
	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		handleGetUsers(w, r, apiTracer, dbTracer, cacheTracer)
	})

	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		handleGetOrders(w, r, apiTracer, dbTracer)
	})

	// Wrap with tracing middleware
	handler := tracing.HTTPMiddleware(apiTracer)(mux)

	// Start HTTP server
	go func() {
		log.Println("🌐 Example API server listening on :8081")
		if err := http.ListenAndServe(":8081", handler); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Simulate traffic
	simulateTraffic(storage)
}

func handleGetUsers(w http.ResponseWriter, r *http.Request, apiTracer, dbTracer, cacheTracer *tracing.Tracer) {
	ctx := r.Context()

	// Check cache first
	cacheCtx, cacheSpan := cacheTracer.StartSpan(ctx, "cache.lookup", tracing.SpanKindInternal)
	time.Sleep(time.Duration(rand.Intn(5)+1) * time.Millisecond) // Simulate cache lookup
	if rand.Float32() < 0.3 {
		// Cache hit
		cacheTracer.SetTag(cacheSpan, "cache.hit", "true")
		cacheTracer.FinishSpan(cacheCtx, cacheSpan)

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"users": [{"id": 1, "name": "John"}]}`)
		return
	}
	cacheTracer.SetTag(cacheSpan, "cache.hit", "false")
	cacheTracer.FinishSpan(cacheCtx, cacheSpan)

	// Cache miss - query database
	dbCtx, dbSpan := dbTracer.StartSpan(ctx, "db.query.users", tracing.SpanKindInternal)
	dbTracer.SetTag(dbSpan, "db.query", "SELECT * FROM users")
	dbTracer.SetTag(dbSpan, "db.table", "users")

	// Simulate database query
	time.Sleep(time.Duration(rand.Intn(20)+10) * time.Millisecond)

	// Simulate occasional database errors
	if rand.Float32() < 0.1 {
		err := fmt.Errorf("connection timeout")
		dbTracer.FinishSpanWithError(dbCtx, dbSpan, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	dbTracer.SetTag(dbSpan, "db.rows_returned", "42")
	dbTracer.FinishSpan(dbCtx, dbSpan)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"users": [{"id": 1, "name": "John"}, {"id": 2, "name": "Jane"}]}`)
}

func handleGetOrders(w http.ResponseWriter, r *http.Request, apiTracer, dbTracer *tracing.Tracer) {
	ctx := r.Context()

	// Query orders
	dbCtx, dbSpan := dbTracer.StartSpan(ctx, "db.query.orders", tracing.SpanKindInternal)
	dbTracer.SetTag(dbSpan, "db.query", "SELECT * FROM orders")
	dbTracer.SetTag(dbSpan, "db.table", "orders")

	// Simulate slow query
	time.Sleep(time.Duration(rand.Intn(50)+20) * time.Millisecond)

	dbTracer.SetTag(dbSpan, "db.rows_returned", "156")
	dbTracer.FinishSpan(dbCtx, dbSpan)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"orders": [{"id": 1, "amount": 99.99}]}`)
}

func simulateTraffic(storage *tracing.Storage) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	endpoints := []string{
		"http://localhost:8081/api/users",
		"http://localhost:8081/api/orders",
	}

	log.Println("🚀 Generating sample traffic...")

	for range ticker.C {
		// Pick random endpoint
		endpoint := endpoints[rand.Intn(len(endpoints))]

		// Make request
		resp, err := client.Get(endpoint)
		if err != nil {
			log.Printf("Request failed: %v", err)
			continue
		}
		resp.Body.Close()

		// Upload traces to TinyObs server
		go uploadTraces(storage)
	}
}

func uploadTraces(storage *tracing.Storage) {
	// Get recent traces
	traces, err := storage.GetRecentTraces(context.Background(), 10)
	if err != nil {
		return
	}

	// Upload each span to TinyObs
	client := &http.Client{Timeout: 5 * time.Second}
	for _, trace := range traces {
		for range trace.Spans {
			// Send to TinyObs server
			// In production, you'd batch these
			// For now, we just store them locally
			_ = client
			// The spans are already in the storage, which is shared
		}
	}
}
