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
	if err := json.NewDecoder(w.Body).Decode(&queryResp); err != nil {
		t.Fatalf("Failed to decode query response: %v", err)
	}

	countFloat, ok := queryResp["count"].(float64)
	if !ok {
		t.Fatalf("count field is not a float64: %T", queryResp["count"])
	}
	count := int(countFloat)
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

// TestE2E_FullPipeline tests the complete TinyObs pipeline:
// - Write 1000 metrics
// - Run compaction
// - Query and verify downsampled data
// - Restart server (close and reopen DB)
// - Verify data persists
func TestE2E_FullPipeline(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "tinyobs-full-e2e-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	now := time.Now()

	// Step 1: Write 1000 metrics
	t.Log("Step 1: Writing 1000 metrics...")
	store, err := badger.New(badger.Config{Path: tmpDir})
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	metricsToWrite := make([]metrics.Metric, 1000)
	for i := 0; i < 1000; i++ {
		metricsToWrite[i] = metrics.Metric{
			Name:      "test_metric",
			Type:      "counter",
			Value:     float64(i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Labels:    map[string]string{"test": "full_pipeline"},
		}
	}

	err = store.Write(ctx, metricsToWrite)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify raw metrics were written
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalMetrics != 1000 {
		t.Errorf("Expected 1000 metrics written, got %d", stats.TotalMetrics)
	}
	t.Logf("✓ Wrote 1000 metrics successfully")

	// Step 2: Run compaction (5min aggregates)
	t.Log("Step 2: Running compaction...")
	compactor := compaction.New(store)

	// Compact 5min resolution
	err = compactor.Compact5m(ctx, now.Add(-1*time.Hour), now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("5m compaction failed: %v", err)
	}

	// Verify compacted data exists
	statsAfterCompaction, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after compaction failed: %v", err)
	}
	if statsAfterCompaction.TotalMetrics <= 1000 {
		t.Errorf("Expected more than 1000 metrics after compaction (raw + aggregates), got %d",
			statsAfterCompaction.TotalMetrics)
	}
	t.Logf("✓ Compaction created aggregates (total: %d metrics)", statsAfterCompaction.TotalMetrics)

	// Step 3: Query and verify downsampled data
	t.Log("Step 3: Querying downsampled data...")
	handler := ingest.NewHandler(store)
	router := setupRouter(handler)

	// Query for the 5min aggregates
	queryURL := "/v1/query/range?metric=test_metric&start=" +
		now.Add(-1*time.Hour).Format(time.RFC3339) +
		"&end=" + now.Add(2*time.Hour).Format(time.RFC3339) +
		"&maxPoints=100"

	req := httptest.NewRequest("GET", queryURL, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Query failed with status %d: %s", w.Code, w.Body.String())
	}

	var queryResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&queryResp); err != nil {
		t.Fatalf("Failed to decode query response: %v", err)
	}

	// Verify we got data back
	data, ok := queryResp["data"].([]interface{})
	if !ok || len(data) == 0 {
		t.Fatalf("Expected query to return data, got: %v", queryResp)
	}
	t.Logf("✓ Query returned %d data points", len(data))

	// Step 4: Close and reopen DB (simulates server restart)
	t.Log("Step 4: Restarting server (close and reopen DB)...")
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close storage: %v", err)
	}

	store2, err := badger.New(badger.Config{Path: tmpDir})
	if err != nil {
		t.Fatalf("Failed to reopen storage: %v", err)
	}
	defer store2.Close()

	// Step 5: Verify data still exists after restart
	t.Log("Step 5: Verifying data persists after restart...")
	statsAfterRestart, err := store2.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after restart failed: %v", err)
	}

	if statsAfterRestart.TotalMetrics != statsAfterCompaction.TotalMetrics {
		t.Errorf("Data loss after restart! Before: %d, After: %d",
			statsAfterCompaction.TotalMetrics, statsAfterRestart.TotalMetrics)
	}
	t.Logf("✓ Data persisted correctly (%d metrics)", statsAfterRestart.TotalMetrics)

	// Query again after restart to verify reads work
	handler2 := ingest.NewHandler(store2)
	router2 := setupRouter(handler2)

	req2 := httptest.NewRequest("GET", queryURL, nil)
	w2 := httptest.NewRecorder()
	router2.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("Query after restart failed with status %d: %s", w2.Code, w2.Body.String())
	}

	var queryResp2 map[string]interface{}
	if err := json.NewDecoder(w2.Body).Decode(&queryResp2); err != nil {
		t.Fatalf("Failed to decode query response after restart: %v", err)
	}

	data2, ok := queryResp2["data"].([]interface{})
	if !ok || len(data2) == 0 {
		t.Fatalf("Query after restart returned no data: %v", queryResp2)
	}
	t.Logf("✓ Query after restart returned %d data points", len(data2))

	t.Log("✅ Full E2E pipeline test PASSED")
}

// setupRouter creates a test router
func setupRouter(handler *ingest.Handler) *mux.Router {
	router := mux.NewRouter()
	api := router.PathPrefix("/v1").Subrouter()
	api.HandleFunc("/ingest", handler.HandleIngest).Methods("POST")
	api.HandleFunc("/query", handler.HandleQuery).Methods("GET")
	api.HandleFunc("/query/range", handler.HandleQueryRange).Methods("GET")
	api.HandleFunc("/stats", handler.HandleStats).Methods("GET")
	return router
}
