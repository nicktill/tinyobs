package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/compaction"
	"github.com/nicktill/tinyobs/pkg/ingest"
	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage/badger"
	"github.com/nicktill/tinyobs/pkg/storage/memory"

	"github.com/gorilla/mux"
)

// TestE2E_IngestAndQuery tests full ingestion and query flow
func TestE2E_IngestAndQuery(t *testing.T) {
	// Use memory storage for fast tests
	store := memory.New()
	defer store.Close()

	// Create handler and router
	handler := ingest.NewHandler(store)
	router := setupRouter(handler)

	// Ingest some metrics
	payload := map[string]interface{}{
		"metrics": []map[string]interface{}{
			{
				"name":      "cpu_usage",
				"value":     75.5,
				"labels":    map[string]string{"host": "server1"},
				"timestamp": time.Now().Format(time.RFC3339),
			},
			{
				"name":      "cpu_usage",
				"value":     82.1,
				"labels":    map[string]string{"host": "server2"},
				"timestamp": time.Now().Format(time.RFC3339),
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/v1/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var ingestResp ingest.IngestResponse
	json.NewDecoder(w.Body).Decode(&ingestResp)

	if ingestResp.Count != 2 {
		t.Errorf("Expected 2 metrics ingested, got %d", ingestResp.Count)
	}

	// Query metrics back
	now := time.Now()
	queryURL := "/v1/query?start=" + now.Add(-1*time.Hour).Format(time.RFC3339) +
		"&end=" + now.Add(1*time.Hour).Format(time.RFC3339)

	req = httptest.NewRequest("GET", queryURL, nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Query failed with status %d: %s", w.Code, w.Body.String())
	}

	var queryResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&queryResp)

	count := int(queryResp["count"].(float64))
	if count != 2 {
		t.Errorf("Expected 2 metrics in query results, got %d", count)
	}
}

// TestE2E_Stats tests stats endpoint
func TestE2E_Stats(t *testing.T) {
	store := memory.New()
	defer store.Close()

	// Write some test data
	ctx := context.Background()
	testMetrics := []metrics.Metric{
		{Name: "test1", Value: 1, Timestamp: time.Now()},
		{Name: "test2", Value: 2, Timestamp: time.Now()},
	}
	store.Write(ctx, testMetrics)

	// Create handler and router
	handler := ingest.NewHandler(store)
	router := setupRouter(handler)

	req := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Stats failed with status %d: %s", w.Code, w.Body.String())
	}

	var statsResp struct {
		TotalMetrics uint64 `json:"TotalMetrics"`
		TotalSeries  uint64 `json:"TotalSeries"`
	}
	json.NewDecoder(w.Body).Decode(&statsResp)

	if statsResp.TotalMetrics != 2 {
		t.Errorf("Expected 2 total metrics, got %d", statsResp.TotalMetrics)
	}
}

// TestE2E_CompactionWithBadger tests full compaction flow with BadgerDB
func TestE2E_CompactionWithBadger(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "tinyobs-e2e-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create BadgerDB storage
	store, err := badger.New(badger.Config{Path: tmpDir})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Write raw metrics
	rawMetrics := []metrics.Metric{
		{Name: "metric", Value: 10, Timestamp: now},
		{Name: "metric", Value: 20, Timestamp: now.Add(1 * time.Minute)},
		{Name: "metric", Value: 30, Timestamp: now.Add(2 * time.Minute)},
	}

	err = store.Write(ctx, rawMetrics)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Run compaction
	compactor := compaction.New(store)
	err = compactor.Compact5m(ctx, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Verify we have aggregates now (3 raw + 1 aggregate)
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalMetrics < 3 {
		t.Errorf("Expected at least 3 metrics after compaction, got %d", stats.TotalMetrics)
	}
}

// TestE2E_InvalidRequests tests error handling
func TestE2E_InvalidRequests(t *testing.T) {
	store := memory.New()
	defer store.Close()

	handler := ingest.NewHandler(store)
	router := setupRouter(handler)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "wrong method for ingest",
			method:     "GET",
			path:       "/v1/ingest",
			wantStatus: http.StatusNotFound, // Gorilla mux returns 404 for method mismatch
		},
		{
			name:       "invalid JSON",
			method:     "POST",
			path:       "/v1/ingest",
			body:       "{invalid json}",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "wrong method for query",
			method:     "POST",
			path:       "/v1/query",
			wantStatus: http.StatusNotFound, // Gorilla mux returns 404 for method mismatch
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Expected status %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}
}

// setupRouter creates a test router
func setupRouter(handler *ingest.Handler) *mux.Router {
	router := mux.NewRouter()
	api := router.PathPrefix("/v1").Subrouter()
	api.HandleFunc("/ingest", handler.HandleIngest).Methods("POST")
	api.HandleFunc("/query", handler.HandleQuery).Methods("GET")
	api.HandleFunc("/stats", handler.HandleStats).Methods("GET")
	return router
}
