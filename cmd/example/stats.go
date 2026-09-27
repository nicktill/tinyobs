package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"
)

const tinyObsURL = "http://localhost:8080"

// handleStats queries TinyObs for real metrics from httpx.Middleware
func handleStats() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestTotal := queryTinyObs("sum(http_requests_total)")
		errorTotal := queryTinyObs(`sum(http_requests_total{status=~"4..|5.."})`)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"requests": %d,
			"errors": %d,
			"active": %d,
			"uptime": "%v"
		}`, requestTotal, errorTotal,
			atomic.LoadInt64(&activeRequests), time.Since(startTime).Round(time.Second))
	}
}

// queryTinyObs runs an instant PromQL query and returns the sum of the
// result values, or 0 if the query fails or matches nothing.
func queryTinyObs(query string) int64 {
	resp, err := http.Get(tinyObsURL + "/api/v1/query?" + url.Values{"query": {query}}.Encode())
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var body struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"` // [timestamp, "value"]
			} `json:"result"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		return 0
	}
	var total int64
	for _, r := range body.Data.Result {
		if s, ok := r.Value[1].(string); ok {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				total += int64(v)
			}
		}
	}
	return total
}
