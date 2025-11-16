package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tinyobs/pkg/sdk/metrics"
)

// Transport defines the interface for sending metrics
type Transport interface {
	Send(ctx context.Context, metrics []metrics.Metric) error
}

// HTTPTransport implements Transport using HTTP
type HTTPTransport struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

// NewHTTP creates a new HTTP transport
func NewHTTP(endpoint, apiKey string) (*HTTPTransport, error) {
	return &HTTPTransport{
		endpoint: endpoint,
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

// Send sends metrics to the ingest endpoint
func (t *HTTPTransport) Send(ctx context.Context, metrics []metrics.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	payload := map[string]interface{}{
		"metrics": metrics,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	return nil
}


