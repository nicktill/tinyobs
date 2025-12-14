package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nicktill/tinyobs/pkg/sdk"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/api/users/123", "/api/users/{id}"},
		{"/posts/456/comments/789", "/posts/{id}/comments/{id}"},
		{"/api/users/550e8400-e29b-41d4-a716-446655440000", "/api/users/{id}"},
		{"/api/users", "/api/users"},
		{"/", "/"},
		{"", ""},
		{"/api/users/123/posts/456", "/api/users/{id}/posts/{id}"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := normalizePath(tt.path)
			if got != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		statusCode:     http.StatusOK,
	}

	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want %d", rw.statusCode, http.StatusNotFound)
	}
	if recorder.Code != http.StatusNotFound {
		t.Errorf("recorder.Code = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestMiddleware(t *testing.T) {
	client, err := sdk.New(sdk.ClientConfig{
		Service:  "test",
		Endpoint: "http://localhost:8080/v1/ingest",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(client)(handler)
	req := httptest.NewRequest("GET", "/api/users/123", nil)
	recorder := httptest.NewRecorder()

	wrapped.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}
