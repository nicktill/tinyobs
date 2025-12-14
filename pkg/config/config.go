package config

import "time"

// Server defaults
const (
	DefaultPort         = "8080"
	DefaultMaxStorageGB = 1
	DefaultMaxMemoryMB  = 48
)

// Compaction intervals
const (
	CompactionInterval = 1 * time.Hour
	BadgerGCInterval   = 10 * time.Minute
)

// Query timeouts and defaults
const (
	QueryDefaultStep   = 15 * time.Second
	QueryDefaultWindow = 1 * time.Hour
	QueryTimeout       = 30 * time.Second
)

// Ingest timeouts and limits
const (
	IngestTimeout               = 5 * time.Second
	IngestQueryTimeout          = 10 * time.Second
	IngestStatsTimeout          = 5 * time.Second
	IngestListTimeout           = 5 * time.Second
	IngestDefaultQueryWindow    = 1 * time.Hour
	IngestDefaultMaxPoints      = 1000
	IngestMaxPointsLimit        = 5000
	IngestMetricsListLimit      = 10000
	IngestMetricsListTimeWindow = 24 * time.Hour
	IngestMaxQueryWindow        = 90 * 24 * time.Hour
)

// Export defaults and limits
const (
	DefaultExportWindow = 24 * time.Hour
	MaxExportWindow     = 30 * 24 * time.Hour
)

// WebSocket configuration
const (
	WSReadBufferSize  = 1024
	WSWriteBufferSize = 1024
	WSBroadcastBuffer = 256
	WSChannelBuffer   = 10
	WSWriteDeadline   = 10 * time.Second
	WSReadDeadline    = 60 * time.Second
	WSPingInterval    = 30 * time.Second
)

// Storage defaults
const (
	DefaultMaxMetrics = 50000
)
