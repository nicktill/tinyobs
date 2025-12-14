package server

import (
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/nicktill/tinyobs/pkg/export"
	"github.com/nicktill/tinyobs/pkg/httpx"
	"github.com/nicktill/tinyobs/pkg/ingest"
	"github.com/nicktill/tinyobs/pkg/query"
	"github.com/nicktill/tinyobs/pkg/server/monitor"
)

var startTime = time.Now()

// StorageUsage represents current storage usage stats.
type StorageUsage struct {
	UsedBytes int64 `json:"used_bytes"`
	MaxBytes  int64 `json:"max_bytes"`
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status     string                   `json:"status"`
	Version    string                   `json:"version"`
	Uptime     string                   `json:"uptime"`
	Compaction monitor.CompactionStatus `json:"compaction"`
}

// handleHealth returns service health status.
func handleHealth(compactionMonitor *monitor.CompactionMonitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		compactionHealthy := compactionMonitor.IsHealthy()
		overallStatus := "healthy"
		statusCode := http.StatusOK

		if !compactionHealthy {
			overallStatus = "degraded"
			statusCode = http.StatusServiceUnavailable
		}

		response := HealthResponse{
			Status:     overallStatus,
			Version:    "1.0.0",
			Uptime:     time.Since(startTime).String(),
			Compaction: compactionMonitor.Status(),
		}

		httpx.RespondJSON(w, statusCode, response)
	}
}

// handleStorageUsage returns current storage usage.
func handleStorageUsage(monitor *monitor.StorageMonitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		usedBytes, err := monitor.GetUsage()
		if err != nil {
			httpx.RespondError(w, http.StatusInternalServerError, err)
			return
		}

		usage := StorageUsage{
			UsedBytes: usedBytes,
			MaxBytes:  monitor.GetLimit(),
		}

		httpx.RespondJSON(w, http.StatusOK, usage)
	}
}

// SetupRoutes configures all HTTP routes for the server.
func SetupRoutes(
	router *mux.Router,
	ingestHandler *ingest.Handler,
	queryHandler *query.Handler,
	exportHandler *export.Handler,
	storageMonitor *monitor.StorageMonitor,
	compactionMonitor *monitor.CompactionMonitor,
	hub *ingest.MetricsHub,
	port string,
) {
	// CORS middleware for API access
	router.Use(corsMiddleware(port))

	// API routes
	api := router.PathPrefix("/v1").Subrouter()

	// Metrics ingestion and querying
	api.HandleFunc("/ingest", ingestHandler.HandleIngest).Methods("POST")
	api.HandleFunc("/query", ingestHandler.HandleQuery).Methods("GET")
	api.HandleFunc("/query/range", ingestHandler.HandleRangeQuery).Methods("GET")
	api.HandleFunc("/query/execute", queryHandler.HandleQueryExecute).Methods("POST")
	api.HandleFunc("/query/instant", queryHandler.HandleQueryInstant).Methods("GET", "POST")

	// Metadata and stats
	api.HandleFunc("/metrics/list", ingestHandler.HandleMetricsList).Methods("GET")
	api.HandleFunc("/stats", ingestHandler.HandleStats).Methods("GET")
	api.HandleFunc("/cardinality", ingestHandler.HandleCardinalityStats).Methods("GET")
	api.HandleFunc("/storage", handleStorageUsage(storageMonitor)).Methods("GET")
	api.HandleFunc("/health", handleHealth(compactionMonitor)).Methods("GET")

	// WebSocket for real-time updates
	api.HandleFunc("/ws", ingestHandler.HandleWebSocket(hub)).Methods("GET")

	// Export/import
	api.HandleFunc("/export", exportHandler.HandleExport).Methods("GET")
	api.HandleFunc("/import", exportHandler.HandleImport).Methods("POST")

	// Serve static files from ./web/ directory
	router.PathPrefix("/web/").Handler(http.StripPrefix("/web/", http.FileServer(http.Dir("./web/"))))

	// Root path serves dashboard.html
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/dashboard.html")
	}).Methods("GET")
}

// corsMiddleware creates CORS middleware that restricts to localhost origins only.
func corsMiddleware(port string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Allow localhost origins for local development
			allowedOrigins := []string{
				"http://localhost:" + port,
				"http://127.0.0.1:" + port,
				"http://localhost:3000",
				"http://127.0.0.1:3000",
			}

			// Check if origin is allowed
			allowed := false
			for _, allowedOrigin := range allowedOrigins {
				if origin == allowedOrigin {
					allowed = true
					break
				}
			}

			// Only set CORS headers for allowed origins
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
