package sdk

import (
	"context"
	"testing"
	"time"
)

func TestClientCreation(t *testing.T) {
	client, err := New(ClientConfig{
		Service:    "test-service",
		Endpoint:   "http://localhost:8080/v1/ingest",
		FlushEvery: 1 * time.Second,
	})

	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client == nil {
		t.Fatal("Client is nil")
	}

	// Test that we can create metrics
	counter := client.Counter("test_counter")
	if counter == nil {
		t.Fatal("Counter is nil")
	}

	gauge := client.Gauge("test_gauge")
	if gauge == nil {
		t.Fatal("Gauge is nil")
	}

	histogram := client.Histogram("test_histogram")
	if histogram == nil {
		t.Fatal("Histogram is nil")
	}
}

func TestClientStartStop(t *testing.T) {
	client, err := New(ClientConfig{
		Service:    "test-service",
		Endpoint:   "http://localhost:8080/v1/ingest",
		FlushEvery: 1 * time.Second,
	})

	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start client
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	// Stop client
	if err := client.Stop(); err != nil {
		t.Fatalf("Failed to stop client: %v", err)
	}
}

func TestMetrics(t *testing.T) {
	client, err := New(ClientConfig{
		Service:    "test-service",
		Endpoint:   "http://localhost:8080/v1/ingest",
		FlushEvery: 1 * time.Second,
	})

	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}
	defer client.Stop()

	// Test counter
	counter := client.Counter("test_counter")
	counter.Inc()
	counter.Add(5)
	counter.Inc("label1", "value1")

	// Test gauge
	gauge := client.Gauge("test_gauge")
	gauge.Set(42)
	gauge.Inc()
	gauge.Dec()
	gauge.Add(10)
	gauge.Sub(5)

	// Test histogram
	histogram := client.Histogram("test_histogram")
	histogram.Observe(0.1)
	histogram.Observe(0.5, "label1", "value1")

	// Give some time for metrics to be sent
	time.Sleep(100 * time.Millisecond)
}
