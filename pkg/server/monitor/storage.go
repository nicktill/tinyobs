package monitor

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StorageMonitor tracks storage usage with caching to avoid expensive filesystem calls.
type StorageMonitor struct {
	dataDir       string
	maxBytes      int64
	cachedUsage   int64
	lastCheck     time.Time
	cacheDuration time.Duration
	mu            sync.Mutex
}

// NewStorageMonitor creates a new storage monitor.
func NewStorageMonitor(dataDir string, maxBytes int64) *StorageMonitor {
	return &StorageMonitor{
		dataDir:       dataDir,
		maxBytes:      maxBytes,
		cacheDuration: 10 * time.Second, // Cache for 10 seconds to avoid expensive disk scans
	}
}

// GetUsage returns current storage usage in bytes (cached).
// The cache is refreshed every 10 seconds to balance accuracy with performance.
func (sm *StorageMonitor) GetUsage() (int64, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Return cached value if still fresh
	if time.Since(sm.lastCheck) < sm.cacheDuration {
		return sm.cachedUsage, nil
	}

	// Cache expired, recalculate
	usage, err := calculateDirSize(sm.dataDir)
	if err != nil {
		return 0, err
	}

	sm.cachedUsage = usage
	sm.lastCheck = time.Now()
	return usage, nil
}

// GetLimit returns the configured storage limit in bytes.
func (sm *StorageMonitor) GetLimit() int64 {
	return sm.maxBytes
}

// calculateDirSize recursively calculates directory size in bytes.
// Uses actual disk usage (not logical size) to handle sparse files correctly.
func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			// Get actual disk usage, not logical file size
			actualSize, err := getActualFileSize(filePath, info)
			if err != nil {
				// Fallback to logical size if we can't get actual size
				size += info.Size()
			} else {
				size += actualSize
			}
		}
		return nil
	})
	return size, err
}

// getActualFileSize is implemented in platform-specific files:
// - filesize_unix.go (Linux/Mac): Uses syscall.Stat_t.Blocks
// - filesize_windows.go (Windows): Uses GetCompressedFileSizeW API
