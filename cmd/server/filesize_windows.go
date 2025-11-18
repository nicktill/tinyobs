//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	getCompressedSize  = kernel32.NewProc("GetCompressedFileSizeW")
)

// getActualFileSize returns actual disk usage in bytes on Windows
// Uses GetCompressedFileSize to handle sparse files correctly
func getActualFileSize(path string, info os.FileInfo) (int64, error) {
	// Convert path to UTF-16 for Windows API
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return info.Size(), nil
	}

	var high uint32
	low, _, err := getCompressedSize.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&high)),
	)

	// Check for errors (INVALID_FILE_SIZE = 0xFFFFFFFF)
	if low == 0xFFFFFFFF {
		// API failed, fallback to logical size
		return info.Size(), nil
	}

	// Combine high and low parts to get actual size
	size := int64(high)<<32 + int64(low)
	return size, nil
}
