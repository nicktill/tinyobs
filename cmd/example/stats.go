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

// tinyObsURL is the TinyObs base URL, set from TINYOBS_ENDPOINT in main.
var tinyObsURL = "http://localhost:8080"

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

// queryTinyObs runs a PromQL instant query against TinyObs and returns the
// sum of the result's values, or 0 if TinyObs is unreachable.
func queryTinyObs(query string) int64 {
	resp, err := http.PostForm(tinyObsURL+"/api/v1/query", url.Values{"query": {query}})
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
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
