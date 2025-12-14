package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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

// queryTinyObs queries TinyObs for metrics and returns the sum of all latest values
// Uses the simple /v1/query endpoint to get the latest counter values
func queryTinyObs(query string) int64 {
	// Use /v1/query/execute with a small time window to get latest values
	// For counters, we need the most recent value, not the value at exactly "now"
	now := time.Now()
	start := now.Add(-5 * time.Minute) // Look back 5 minutes to ensure we get data

	reqBody := fmt.Sprintf(`{"query":"%s","start":"%s","end":"%s"}`,
		query,
		start.Format(time.RFC3339),
		now.Format(time.RFC3339))

	resp, err := http.Post(tinyObsURL+"/v1/query/execute", "application/json", strings.NewReader(reqBody))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0
	}

	var queryResp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Values [][]interface{} `json:"values"` // [[timestamp, value], ...]
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&queryResp); err != nil {
		return 0
	}

	// If query returned an error, return 0
	if queryResp.Error != "" {
		return 0
	}

	var total int64
	for _, series := range queryResp.Data.Result {
		// For range queries, Values contains multiple entries: [[timestamp, value], ...]
		// Take the last value (most recent) for each series
		if len(series.Values) > 0 {
			// Get the last value in the array (most recent)
			lastValue := series.Values[len(series.Values)-1]
			if len(lastValue) >= 2 {
				if val, ok := lastValue[1].(string); ok {
					if parsed, err := strconv.ParseFloat(val, 64); err == nil {
						total += int64(parsed)
					}
				}
			}
		}
	}

	return total
}
