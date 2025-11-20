//go:build !windows

package main

import (
	"os"
	"syscall"
)

// getActualFileSize returns actual disk usage in bytes on Unix systems
// Uses stat blocks to handle sparse files correctly
func getActualFileSize(path string, info os.FileInfo) (int64, error) {
	sys := info.Sys()
	if sys == nil {
		return info.Size(), nil
	}

	// On Unix systems, get actual blocks allocated
	stat, ok := sys.(*syscall.Stat_t)
	if !ok {
		return info.Size(), nil
	}

	// Blocks are typically 512 bytes each
	// This gives actual disk usage, not logical size
	return stat.Blocks * 512, nil
}
