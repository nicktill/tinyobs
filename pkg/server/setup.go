package server

import (
	"log"
	"os"
	"strconv"

	"github.com/nicktill/tinyobs/pkg/compaction"
	"github.com/nicktill/tinyobs/pkg/config"
	"github.com/nicktill/tinyobs/pkg/export"
	"github.com/nicktill/tinyobs/pkg/ingest"
	"github.com/nicktill/tinyobs/pkg/query"
	"github.com/nicktill/tinyobs/pkg/server/monitor"
	"github.com/nicktill/tinyobs/pkg/storage"
	"github.com/nicktill/tinyobs/pkg/storage/badger"
)

// Config holds server configuration.
type Config struct {
	MaxStorageGB int64
	MaxMemoryMB  int64
	DataDir      string
	Port         string
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() Config {
	maxStorageGB := getEnvInt64("TINYOBS_MAX_STORAGE_GB", config.DefaultMaxStorageGB)
	maxMemoryMB := getEnvInt64("TINYOBS_MAX_MEMORY_MB", config.DefaultMaxMemoryMB)
	port := getPort()

	// Ensure data directory exists
	dataDir := "./data/tinyobs"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	return Config{
		MaxStorageGB: maxStorageGB,
		MaxMemoryMB:  maxMemoryMB,
		DataDir:      dataDir,
		Port:         port,
	}
}

// InitializeStorage initializes BadgerDB storage with the given configuration.
func InitializeStorage(cfg Config) (storage.Storage, error) {
	log.Println("Initializing BadgerDB storage with Snappy compression...")
	store, err := badger.New(badger.Config{
		Path:        cfg.DataDir,
		MaxMemoryMB: cfg.MaxMemoryMB,
	})
	if err != nil {
		return nil, err
	}
	log.Println("BadgerDB storage initialized successfully")
	return store, nil
}

// InitializeHandlers creates and configures all request handlers.
func InitializeHandlers(
	store storage.Storage,
	storageMonitor *monitor.StorageMonitor,
) (
	*ingest.Handler,
	*query.Handler,
	*export.Handler,
	*ingest.MetricsHub,
) {
	// Create ingest handler
	ingestHandler := ingest.NewHandler(store)
	ingestHandler.SetStorageChecker(storageMonitor)
	log.Println("Ingest handler created with cardinality protection & storage limits")

	// Create query handler
	queryHandler := query.NewHandler(store)
	log.Println("Query handler created")

	// Create export/import handler for backup & restore
	exportHandler := export.NewHandler(store)
	log.Println("Export/Import handler created (JSON & CSV backup support)")

	// Create WebSocket hub for real-time updates
	hub := ingest.NewMetricsHub()
	log.Println("WebSocket hub created for real-time metrics streaming")

	return ingestHandler, queryHandler, exportHandler, hub
}

// InitializeCompactor creates a compactor with health monitoring.
func InitializeCompactor(store storage.Storage) (*compaction.Compactor, *monitor.CompactionMonitor) {
	compactor := compaction.New(store)
	compactionMonitor := &monitor.CompactionMonitor{}
	log.Printf("Compaction engine ready (runs every %v)", config.CompactionInterval)
	return compactor, compactionMonitor
}

// getEnvInt64 gets an int64 from environment variable or returns default.
func getEnvInt64(key string, defaultValue int64) int64 {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.ParseInt(val, 10, 64); err == nil {
			return parsed
		}
		log.Printf("Invalid value for %s: %q, using default %d", key, val, defaultValue)
	}
	return defaultValue
}

// getPort gets the server port from PORT environment variable or returns default.
func getPort() string {
	if port := os.Getenv("PORT"); port != "" {
		return port
	}
	return config.DefaultPort
}
