//go:build !windows

package main

import (
	"os"
	"syscall"
)

// getFileSize returns actual disk usage in bytes (handles sparse files correctly)
func getFileSize(info os.FileInfo) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok {
		// Blocks are 512 bytes on Unix systems
		return stat.Blocks * 512
	}
	// Fallback to logical size
	return info.Size()
}
