package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

func TestNewHTTP(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		apiKey   string
		wantErr  bool
	}{
		{
			name:     "valid endpoint without API key",
			endpoint: "http://localhost:8080/v1/ingest",
			apiKey:   "",
			wantErr:  false,
		},
		{
			name:     "valid endpoint with API key",
			endpoint: "http://localhost:8080/v1/ingest",
			apiKey:   "secret-key",
			wantErr:  false,
		},
		{
			name:     "empty endpoint",
			endpoint: "",
			apiKey:   "",
			wantErr:  false, // NewHTTP doesn't validate endpoint currently
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, err := NewHTTP(tt.endpoint, tt.apiKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewHTTP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if transport.endpoint != tt.endpoint {
					t.Errorf("endpoint = %v, want %v", transport.endpoint, tt.endpoint)
				}
				if transport.apiKey != tt.apiKey {
					t.Errorf("apiKey = %v, want %v", transport.apiKey, tt.apiKey)
				}
				if transport.client == nil {
					t.Error("HTTP client is nil")
				}
				if transport.client.Timeout != 10*time.Second {
					t.Errorf("timeout = %v, want %v", transport.client.Timeout, 10*time.Second)
				}
			}
		})
	}
}

func TestHTTPTransport_Send_Success(t *testing.T) {
	// Create test server
	var receivedPayload map[string]interface{}
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("method = %v, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %v, want application/json", r.Header.Get("Content-Type"))
		}

		receivedAuth = r.Header.Get("Authorization")

		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}

		// Parse JSON
		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Fatalf("failed to parse JSON: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create transport
	transport, err := NewHTTP(server.URL, "test-api-key")
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	// Send metrics
	testMetrics := []metrics.Metric{
		{
			Name:      "test_counter",
			Type:      "counter",
			Value:     42.0,
			Timestamp: time.Now(),
			Labels: map[string]string{
				"service": "test",
			},
		},
	}

	ctx := context.Background()
	if err := transport.Send(ctx, testMetrics); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Verify received data
	if receivedPayload == nil {
		t.Fatal("server did not receive payload")
	}

	metricsData, ok := receivedPayload["metrics"].([]interface{})
	if !ok {
		t.Fatal("payload does not contain metrics array")
	}

	if len(metricsData) != 1 {
		t.Errorf("received %d metrics, want 1", len(metricsData))
	}

	// Verify API key was sent
	if receivedAuth != "Bearer test-api-key" {
		t.Errorf("Authorization = %v, want Bearer test-api-key", receivedAuth)
	}
}

func TestHTTPTransport_Send_EmptyMetrics(t *testing.T) {
	// Create transport (endpoint doesn't matter since we won't call it)
	transport, err := NewHTTP("http://localhost:8080/v1/ingest", "")
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	ctx := context.Background()
	err = transport.Send(ctx, []metrics.Metric{})
	if err != nil {
		t.Errorf("Send() with empty metrics should not error, got: %v", err)
	}

	// Also test nil metrics
	err = transport.Send(ctx, nil)
	if err != nil {
		t.Errorf("Send() with nil metrics should not error, got: %v", err)
	}
}

func TestHTTPTransport_Send_HTTPErrors(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		expectError    bool
		errorSubstring string
	}{
		{
			name:        "200 OK",
			statusCode:  http.StatusOK,
			expectError: false,
		},
		{
			name:        "201 Created",
			statusCode:  http.StatusCreated,
			expectError: false,
		},
		{
			name:           "400 Bad Request",
			statusCode:     http.StatusBadRequest,
			expectError:    true,
			errorSubstring: "status 400",
		},
		{
			name:           "401 Unauthorized",
			statusCode:     http.StatusUnauthorized,
			expectError:    true,
			errorSubstring: "status 401",
		},
		{
			name:           "500 Internal Server Error",
			statusCode:     http.StatusInternalServerError,
			expectError:    true,
			errorSubstring: "status 500",
		},
		{
			name:           "503 Service Unavailable",
			statusCode:     http.StatusServiceUnavailable,
			expectError:    true,
			errorSubstring: "status 503",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			transport, err := NewHTTP(server.URL, "")
			if err != nil {
				t.Fatalf("NewHTTP() error = %v", err)
			}

			testMetrics := []metrics.Metric{
				{Name: "test", Type: "counter", Value: 1.0, Timestamp: time.Now()},
			}

			ctx := context.Background()
			err = transport.Send(ctx, testMetrics)

			if tt.expectError {
				if err == nil {
					t.Errorf("Send() expected error for status %d, got nil", tt.statusCode)
				} else if tt.errorSubstring != "" {
					if !contains(err.Error(), tt.errorSubstring) {
						t.Errorf("error = %v, want substring %v", err, tt.errorSubstring)
					}
				}
			} else {
				if err != nil {
					t.Errorf("Send() unexpected error for status %d: %v", tt.statusCode, err)
				}
			}
		})
	}
}

func TestHTTPTransport_Send_NetworkError(t *testing.T) {
	// Use an invalid endpoint that will cause network error
	transport, err := NewHTTP("http://localhost:1", "") // Port 1 is unlikely to be listening
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	// Reduce timeout for faster test
	transport.client.Timeout = 100 * time.Millisecond

	testMetrics := []metrics.Metric{
		{Name: "test", Type: "counter", Value: 1.0, Timestamp: time.Now()},
	}

	ctx := context.Background()
	err = transport.Send(ctx, testMetrics)
	if err == nil {
		t.Error("Send() expected network error, got nil")
	}
	if !contains(err.Error(), "failed to send request") {
		t.Errorf("error = %v, want 'failed to send request'", err)
	}
}

func TestHTTPTransport_Send_ContextCancellation(t *testing.T) {
	// Create server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport, err := NewHTTP(server.URL, "")
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	testMetrics := []metrics.Metric{
		{Name: "test", Type: "counter", Value: 1.0, Timestamp: time.Now()},
	}

	// Create context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = transport.Send(ctx, testMetrics)
	if err == nil {
		t.Error("Send() expected context cancellation error, got nil")
	}
	if !contains(err.Error(), "context canceled") && !contains(err.Error(), "failed to send request") {
		t.Errorf("error = %v, expected context cancellation error", err)
	}
}

func TestHTTPTransport_Send_NoAPIKey(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport, err := NewHTTP(server.URL, "") // No API key
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	testMetrics := []metrics.Metric{
		{Name: "test", Type: "counter", Value: 1.0, Timestamp: time.Now()},
	}

	ctx := context.Background()
	if err := transport.Send(ctx, testMetrics); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Verify no Authorization header was sent
	if receivedAuth != "" {
		t.Errorf("Authorization header = %v, want empty", receivedAuth)
	}
}

func TestHTTPTransport_Send_Timeout(t *testing.T) {
	// Create server that never responds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Second) // Sleep longer than timeout
	}))
	defer server.Close()

	transport, err := NewHTTP(server.URL, "")
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	// Reduce timeout for faster test
	transport.client.Timeout = 100 * time.Millisecond

	testMetrics := []metrics.Metric{
		{Name: "test", Type: "counter", Value: 1.0, Timestamp: time.Now()},
	}

	ctx := context.Background()
	err = transport.Send(ctx, testMetrics)
	if err == nil {
		t.Error("Send() expected timeout error, got nil")
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
