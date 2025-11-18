//go:build windows

package main

import (
	"os"
)

// getFileSize returns file size in bytes
// Note: Windows doesn't expose block-level size via os.FileInfo
// This may overreport for sparse files, but is the best we can do portably
func getFileSize(info os.FileInfo) int64 {
	return info.Size()
}
