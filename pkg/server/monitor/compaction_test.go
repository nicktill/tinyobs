package monitor

import (
	"errors"
	"testing"
	"time"
)

func TestCompactionMonitor_RecordSuccess(t *testing.T) {
	cm := &CompactionMonitor{}
	cm.RecordSuccess()

	status := cm.Status()
	if !status.Healthy {
		t.Error("Status should be healthy after success")
	}
	if status.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors = %d, want 0", status.ConsecutiveErrors)
	}
	if status.LastError != "" {
		t.Errorf("LastError = %q, want empty", status.LastError)
	}
}

func TestCompactionMonitor_RecordFailure(t *testing.T) {
	cm := &CompactionMonitor{}
	cm.RecordFailure(errors.New("disk full"))

	status := cm.Status()
	if status.ConsecutiveErrors != 1 {
		t.Errorf("ConsecutiveErrors = %d, want 1", status.ConsecutiveErrors)
	}
	if status.LastError != "disk full" {
		t.Errorf("LastError = %q, want %q", status.LastError, "disk full")
	}
}

func TestCompactionMonitor_IsHealthy(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*CompactionMonitor)
		expected bool
	}{
		{
			name:     "never succeeded",
			setup:    func(*CompactionMonitor) {},
			expected: false,
		},
		{
			name: "recent success",
			setup: func(cm *CompactionMonitor) {
				cm.RecordSuccess()
			},
			expected: true,
		},
		{
			name: "stale success",
			setup: func(cm *CompactionMonitor) {
				cm.mu.Lock()
				cm.lastSuccess = time.Now().Add(-2 * time.Hour)
				cm.mu.Unlock()
			},
			expected: false,
		},
		{
			name: "too many consecutive errors",
			setup: func(cm *CompactionMonitor) {
				cm.RecordSuccess()
				cm.RecordFailure(errors.New("error 1"))
				cm.RecordFailure(errors.New("error 2"))
				cm.RecordFailure(errors.New("error 3"))
				cm.RecordFailure(errors.New("error 4"))
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &CompactionMonitor{}
			tt.setup(cm)
			if got := cm.IsHealthy(); got != tt.expected {
				t.Errorf("IsHealthy() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCompactionMonitor_Status(t *testing.T) {
	cm := &CompactionMonitor{}
	cm.RecordSuccess()

	status := cm.Status()
	if !status.Healthy {
		t.Error("Status should be healthy")
	}
	if status.LastSuccess == "" {
		t.Error("LastSuccess should be set")
	}
	if status.TimeSinceSuccess == "" {
		t.Error("TimeSinceSuccess should be set")
	}
}
