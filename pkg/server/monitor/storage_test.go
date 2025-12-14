package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorageMonitor_GetLimit(t *testing.T) {
	sm := NewStorageMonitor("/tmp", 1024*1024*1024)
	if got := sm.GetLimit(); got != 1024*1024*1024 {
		t.Errorf("GetLimit() = %d, want %d", got, 1024*1024*1024)
	}
}

func TestStorageMonitor_GetUsage(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test data"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	sm := NewStorageMonitor(tmpDir, 1024*1024*1024)
	usage, err := sm.GetUsage()
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}

	if usage < 9 {
		t.Errorf("GetUsage() = %d, want at least 9", usage)
	}
}

func TestStorageMonitor_Caching(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewStorageMonitor(tmpDir, 1024*1024*1024)

	usage1, err := sm.GetUsage()
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}

	usage2, err := sm.GetUsage()
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}

	if usage1 != usage2 {
		t.Errorf("Cached values differ: %d != %d", usage1, usage2)
	}
}

func TestStorageMonitor_InvalidDir(t *testing.T) {
	sm := NewStorageMonitor("/nonexistent/path/12345", 1024*1024*1024)
	_, err := sm.GetUsage()
	if err == nil {
		t.Error("GetUsage() should return error for nonexistent directory")
	}
}
