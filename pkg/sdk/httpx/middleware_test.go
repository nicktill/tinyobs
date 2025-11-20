package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// mockClient implements metrics.ClientInterface for testing
type mockClient struct {
	metricsReceived []metrics.Metric
}

func newMockClient() *mockClient {
	return &mockClient{
		metricsReceived: make([]metrics.Metric, 0),
	}
}

func (m *mockClient) SendMetric(metric metrics.Metric) {
	m.metricsReceived = append(m.metricsReceived, metric)
}

// Helper methods for test assertions
func (m *mockClient) countMetrics(name string, metricType metrics.MetricType, labels map[string]string) int {
	count := 0
	for _, metric := range m.metricsReceived {
		if metric.Name == name && metric.Type == metricType {
			if labelsMatch(metric.Labels, labels) {
				count++
			}
		}
	}
	return count
}

func (m *mockClient) getFinalCounterValue(name string, labels map[string]string) float64 {
	// Counter sends cumulative values, so we want the last (highest) value
	var finalValue float64
	for _, metric := range m.metricsReceived {
		if metric.Name == name && metric.Type == metrics.CounterType {
			if labelsMatch(metric.Labels, labels) {
				if metric.Value > finalValue {
					finalValue = metric.Value
				}
			}
		}
	}
	return finalValue
}

func labelsMatch(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestMiddleware_BasicRequest(t *testing.T) {
	client := newMockClient()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := Middleware(client)
	wrapped := middleware(handler)

	req := httptest.NewRequest("GET", "/api/users", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// Verify counter was recorded
	labels := map[string]string{
		"method": "GET",
		"path":   "/api/users",
		"status": "200",
	}
	counterCount := client.countMetrics("http_requests_total", metrics.CounterType, labels)
	if counterCount != 1 {
		t.Errorf("Expected 1 counter metric, got %d. Metrics: %v", counterCount, client.metricsReceived)
	}

	finalValue := client.getFinalCounterValue("http_requests_total", labels)
	if finalValue != 1.0 {
		t.Errorf("Expected final counter value 1, got %f", finalValue)
	}
}

func TestMiddleware_ErrorStatus(t *testing.T) {
	client := newMockClient()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	})

	middleware := Middleware(client)
	wrapped := middleware(handler)

	req := httptest.NewRequest("POST", "/api/create", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	// Verify response
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", rec.Code)
	}

	// Verify counter recorded with status=500
	labels := map[string]string{
		"method": "POST",
		"path":   "/api/create",
		"status": "500",
	}
	counterCount := client.countMetrics("http_requests_total", metrics.CounterType, labels)
	if counterCount != 1 {
		t.Errorf("Expected 1 counter metric, got %d. Metrics: %v", counterCount, client.metricsReceived)
	}
}

func TestMiddleware_MultipleRequests(t *testing.T) {
	client := newMockClient()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(client)
	wrapped := middleware(handler)

	// Make 3 requests
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}

	// Verify counter was incremented 3 times
	labels := map[string]string{
		"method": "GET",
		"path":   "/health",
		"status": "200",
	}
	counterCount := client.countMetrics("http_requests_total", metrics.CounterType, labels)
	if counterCount != 3 {
		t.Errorf("Expected 3 counter metrics, got %d", counterCount)
	}

	// Verify final counter value is 3 (cumulative)
	finalValue := client.getFinalCounterValue("http_requests_total", labels)
	if finalValue != 3.0 {
		t.Errorf("Expected final counter value 3, got %f", finalValue)
	}
}

func TestMiddleware_DifferentPaths(t *testing.T) {
	client := newMockClient()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(client)
	wrapped := middleware(handler)

	paths := []string{"/api/users", "/api/posts", "/health"}
	for _, path := range paths {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}

	// Verify each path has its own counter
	for _, path := range paths {
		labels := map[string]string{
			"method": "GET",
			"path":   path,
			"status": "200",
		}
		counterCount := client.countMetrics("http_requests_total", metrics.CounterType, labels)
		if counterCount != 1 {
			t.Errorf("Expected 1 counter for path %s, got %d", path, counterCount)
		}
	}

	// Verify we received exactly 3 counter metrics
	totalCounters := 0
	for _, metric := range client.metricsReceived {
		if metric.Type == metrics.CounterType {
			totalCounters++
		}
	}
	if totalCounters != 3 {
		t.Errorf("Expected 3 counter metrics, got %d: %v", totalCounters, client.metricsReceived)
	}
}

func TestResponseWriter_CapturesStatusCode(t *testing.T) {
	// Test the responseWriter wrapper directly
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	// WriteHeader should capture the status code
	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code 404, got %d", rw.statusCode)
	}

	if rec.Code != http.StatusNotFound {
		t.Errorf("Expected underlying recorder to have status 404, got %d", rec.Code)
	}
}

func TestResponseWriter_DefaultStatusOK(t *testing.T) {
	// Test that default status is 200 OK when WriteHeader is not called
	client := newMockClient()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't call WriteHeader, just write body
		w.Write([]byte("OK"))
	})

	middleware := Middleware(client)
	wrapped := middleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	// Verify counter recorded with status=200 (default)
	labels := map[string]string{
		"method": "GET",
		"path":   "/test",
		"status": "200",
	}
	counterCount := client.countMetrics("http_requests_total", metrics.CounterType, labels)
	if counterCount != 1 {
		t.Errorf("Expected 1 counter metric with default status 200, got %d. Metrics: %v", counterCount, client.metricsReceived)
	}
}
