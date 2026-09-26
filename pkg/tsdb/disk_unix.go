//go:build unix

package tsdb

import (
	"io/fs"
	"syscall"
)

// allocated returns the disk space a file occupies, which for Badger's
// preallocated (sparse) value log is far less than its apparent size.
func allocated(fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}
