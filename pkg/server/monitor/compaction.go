package monitor

import (
	"sync"
	"time"
)

// CompactionMonitor tracks compaction health and failures.
type CompactionMonitor struct {
	mu                sync.RWMutex
	lastSuccess       time.Time
	lastAttempt       time.Time
	consecutiveErrors int
	lastError         string
}

// RecordSuccess records a successful compaction.
func (cm *CompactionMonitor) RecordSuccess() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastSuccess = time.Now()
	cm.lastAttempt = time.Now()
	cm.consecutiveErrors = 0
	cm.lastError = ""
}

// RecordFailure records a failed compaction.
func (cm *CompactionMonitor) RecordFailure(err error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastAttempt = time.Now()
	cm.consecutiveErrors++
	if err != nil {
		cm.lastError = err.Error()
	}
}

// IsHealthy returns true if compaction is working properly.
// Unhealthy conditions:
//   - Never succeeded
//   - Haven't succeeded in >1 hour
//   - More than 3 consecutive failures
func (cm *CompactionMonitor) IsHealthy() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.lastSuccess.IsZero() {
		return false
	}
	if time.Since(cm.lastSuccess) > 1*time.Hour {
		return false
	}
	if cm.consecutiveErrors > 3 {
		return false
	}
	return true
}

// Status returns current compaction status for health checks.
type CompactionStatus struct {
	Healthy           bool   `json:"healthy"`
	LastSuccess       string `json:"last_success,omitempty"`
	TimeSinceSuccess  string `json:"time_since_success,omitempty"`
	LastAttempt       string `json:"last_attempt,omitempty"`
	ConsecutiveErrors int    `json:"consecutive_errors,omitempty"`
	LastError         string `json:"last_error,omitempty"`
}

// Status returns current compaction status for health checks.
func (cm *CompactionMonitor) Status() CompactionStatus {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	status := CompactionStatus{
		Healthy: cm.IsHealthy(),
	}

	if !cm.lastSuccess.IsZero() {
		status.LastSuccess = cm.lastSuccess.Format(time.RFC3339)
		status.TimeSinceSuccess = time.Since(cm.lastSuccess).String()
	}

	if !cm.lastAttempt.IsZero() {
		status.LastAttempt = cm.lastAttempt.Format(time.RFC3339)
	}

	if cm.consecutiveErrors > 0 {
		status.ConsecutiveErrors = cm.consecutiveErrors
		status.LastError = cm.lastError
	}

	return status
}
