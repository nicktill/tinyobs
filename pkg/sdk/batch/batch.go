package batch

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/sdk/transport"
)

// Config holds configuration for the batcher
type Config struct {
	MaxBatchSize int
	FlushEvery   time.Duration
}

// Batcher batches metrics and sends them periodically
type Batcher struct {
	config    Config
	transport transport.Transport

	metrics []metrics.Metric
	mu      sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	flushing atomic.Bool // Prevents concurrent flushes (HIGH: prevents unbounded goroutine spawning)
}

// New creates a new batcher
func New(transport transport.Transport, config Config) *Batcher {
	return &Batcher{
		config:    config,
		transport: transport,
		metrics:   make([]metrics.Metric, 0, config.MaxBatchSize),
		done:      make(chan struct{}),
	}
}

// Start starts the batcher
func (b *Batcher) Start(ctx context.Context) error {
	b.ctx, b.cancel = context.WithCancel(ctx)

	go b.flushLoop()
	return nil
}

// Add adds a metric to the batch
// CRITICAL: Uses atomic flag to prevent unbounded goroutine spawning under high load
func (b *Batcher) Add(metric metrics.Metric) {
	b.mu.Lock()
	b.metrics = append(b.metrics, metric)
	shouldFlush := len(b.metrics) >= b.config.MaxBatchSize
	b.mu.Unlock()

	// Flush if batch is full AND no flush is already running
	// CompareAndSwap ensures only one flush goroutine runs at a time
	if shouldFlush && b.flushing.CompareAndSwap(false, true) {
		go func() {
			b.flush()
			b.flushing.Store(false)
		}()
	}
}

// Flush flushes all pending metrics
func (b *Batcher) Flush() error {
	b.mu.Lock()
	if len(b.metrics) == 0 {
		b.mu.Unlock()
		return nil
	}

	metrics := make([]metrics.Metric, len(b.metrics))
	copy(metrics, b.metrics)
	b.metrics = b.metrics[:0]
	b.mu.Unlock()

	return b.sendMetrics(metrics)
}

// Stop stops the batcher
func (b *Batcher) Stop() error {
	if b.cancel != nil {
		b.cancel()
	}

	// Wait for flush loop to finish
	<-b.done

	// Flush remaining metrics
	return b.Flush()
}

// flushLoop periodically flushes metrics
// CRITICAL: Uses atomic flag to prevent concurrent flushes with Add()
func (b *Batcher) flushLoop() {
	defer close(b.done)

	ticker := time.NewTicker(b.config.FlushEvery)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			// Only flush if no flush is already running
			if b.flushing.CompareAndSwap(false, true) {
				b.flush()
				b.flushing.Store(false)
			}
		}
	}
}

// flush flushes metrics without blocking
func (b *Batcher) flush() {
	b.mu.Lock()
	if len(b.metrics) == 0 {
		b.mu.Unlock()
		return
	}

	metrics := make([]metrics.Metric, len(b.metrics))
	copy(metrics, b.metrics)
	b.metrics = b.metrics[:0]
	b.mu.Unlock()

	// Send in background to avoid blocking
	go b.sendMetrics(metrics)
}

// sendMetrics sends metrics via transport
func (b *Batcher) sendMetrics(metrics []metrics.Metric) error {
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()

	return b.transport.Send(ctx, metrics)
}
