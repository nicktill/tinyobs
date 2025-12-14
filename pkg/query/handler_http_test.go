package query

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nicktill/tinyobs/pkg/storage/memory"
	"github.com/stretchr/testify/require"
)

func TestHandlePrometheusQueryRange_MissingQuery(t *testing.T) {
	handler := NewHandler(memory.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range", nil)
	rr := httptest.NewRecorder()

	handler.HandlePrometheusQueryRange(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Contains(t, resp["message"], "query parameter is required")
}

func TestHandlePrometheusQueryRange_InvalidRange(t *testing.T) {
	handler := NewHandler(memory.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?query=up&start=2000&end=1000", nil)
	rr := httptest.NewRecorder()

	handler.HandlePrometheusQueryRange(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Contains(t, resp["message"], "start must be before end")
}
