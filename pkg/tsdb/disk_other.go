//go:build !unix

package tsdb

import "io/fs"

func allocated(fi fs.FileInfo) int64 { return fi.Size() }
