package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage/memory"
	"github.com/stretchr/testify/require"
)

func TestHandleIngest_TooManyMetrics(t *testing.T) {
	store := memory.New()
	handler := NewHandler(store)

	metricsPayload := make([]metrics.Metric, MaxMetricsPerRequest+1)
	for i := range metricsPayload {
		metricsPayload[i] = metrics.Metric{Name: "test_metric"}
	}
	body, err := json.Marshal(IngestRequest{Metrics: metricsPayload})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/ingest", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	handler.HandleIngest(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Contains(t, resp["message"], "too many metrics")
}

func TestHandleIngest_InvalidMetric(t *testing.T) {
	store := memory.New()
	handler := NewHandler(store)

	payload := IngestRequest{
		Metrics: []metrics.Metric{
			{Name: ""}, // invalid name
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/ingest", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	handler.HandleIngest(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Contains(t, resp["message"], "invalid metric")
}
