package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
	"github.com/nicktill/tinyobs/pkg/storage"
)

// TopologyNode represents a service in the topology
type TopologyNode struct {
	ID          string  `json:"id"`           // Service name
	Label       string  `json:"label"`        // Display name
	RequestRate float64 `json:"request_rate"` // Requests per second
	ErrorRate   float64 `json:"error_rate"`   // Error rate (0-1)
	Type        string  `json:"type"`         // "service", "database", "external"
}

// TopologyEdge represents a connection between services
type TopologyEdge struct {
	Source      string  `json:"source"`       // Source service ID
	Target      string  `json:"target"`       // Target service ID
	RequestRate float64 `json:"request_rate"` // Requests per second
	ErrorRate   float64 `json:"error_rate"`   // Error rate (0-1)
	Latency     float64 `json:"latency"`      // Average latency in ms
}

// TopologyResponse represents the topology graph
type TopologyResponse struct {
	Nodes         []TopologyNode `json:"nodes"`
	Edges         []TopologyEdge `json:"edges"`
	LastUpdated   string         `json:"last_updated"`
	TimeRangeHours float64       `json:"time_range_hours"`
}

// HandleTopology handles the /v1/topology endpoint
// Analyzes metrics to build service dependency graph
func (h *Handler) HandleTopology(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	// Query recent metrics (last 1 hour by default)
	timeRange := 1 * time.Hour
	if rangeParam := r.URL.Query().Get("hours"); rangeParam != "" {
		if hours, err := time.ParseDuration(rangeParam + "h"); err == nil {
			timeRange = hours
		}
	}

	start := time.Now().Add(-timeRange)
	end := time.Now()

	// Query all metrics in time range
	metrics, err := h.storage.Query(ctx, storage.QueryRequest{
		Start: start,
		End:   end,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query metrics: %v", err), http.StatusInternalServerError)
		return
	}

	// Build topology from metrics
	topology := buildTopology(metrics, timeRange)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(topology); err != nil {
		log.Printf("❌ Failed to encode topology response: %v", err)
	}
}

// buildTopology analyzes metrics and constructs service topology
func buildTopology(metricsList []metrics.Metric, timeRange time.Duration) TopologyResponse {
	nodes := make(map[string]*TopologyNode)
	edges := make(map[string]*TopologyEdge)

	hours := timeRange.Hours()

	for _, metric := range metricsList {
		name := metric.Name
		value := metric.Value
		labels := metric.Labels

		// Extract service name
		service := labels["service"]
		if service == "" {
			continue
		}

		// Create or update node
		if _, exists := nodes[service]; !exists {
			nodes[service] = &TopologyNode{
				ID:    service,
				Label: service,
				Type:  "service",
			}
		}

		// Track request metrics
		if strings.Contains(name, "http_requests") || strings.Contains(name, "requests_total") {
			nodes[service].RequestRate += value / hours
		}

		// Track error rates
		if strings.Contains(name, "errors") || strings.Contains(name, "failures") {
			nodes[service].ErrorRate += value
		}

		// Detect service-to-service communication
		// Look for patterns like:
		// 1. endpoint="/api/service-name/..." (calling another service)
		// 2. target_service="..." (explicit target)
		// 3. upstream="..." (upstream service)

		var target string

		// Check for explicit target labels
		if targetService := labels["target_service"]; targetService != "" {
			target = targetService
		} else if upstream := labels["upstream"]; upstream != "" {
			target = upstream
		} else if endpoint := labels["endpoint"]; endpoint != "" {
			// Try to extract service name from endpoint
			// e.g., "/api/users-service/..." → "users-service"
			target = extractServiceFromEndpoint(endpoint)
		}

		if target != "" && target != service {
			// Create edge
			edgeKey := service + "->" + target
			if _, exists := edges[edgeKey]; !exists {
				edges[edgeKey] = &TopologyEdge{
					Source: service,
					Target: target,
				}
			}

			// Aggregate metrics for edge
			if strings.Contains(name, "http_requests") || strings.Contains(name, "requests_total") {
				edges[edgeKey].RequestRate += value / hours
			}

			if strings.Contains(name, "duration") || strings.Contains(name, "latency") {
				edges[edgeKey].Latency += value
			}

			// Ensure target node exists
			if _, exists := nodes[target]; !exists {
				nodes[target] = &TopologyNode{
					ID:    target,
					Label: target,
					Type:  detectServiceType(target),
				}
			}
		}
	}

	// Calculate error rates (as percentage)
	for _, node := range nodes {
		if node.RequestRate > 0 {
			node.ErrorRate = node.ErrorRate / node.RequestRate
		}
	}

	// Convert maps to slices
	nodeSlice := make([]TopologyNode, 0, len(nodes))
	for _, node := range nodes {
		nodeSlice = append(nodeSlice, *node)
	}

	edgeSlice := make([]TopologyEdge, 0, len(edges))
	for _, edge := range edges {
		// Calculate error rate for edge
		if edge.RequestRate > 0 {
			// Error rate is already aggregated in Latency field temporarily
			edge.ErrorRate = 0 // Reset for now
		}
		edgeSlice = append(edgeSlice, *edge)
	}

	return TopologyResponse{
		Nodes:          nodeSlice,
		Edges:          edgeSlice,
		LastUpdated:    time.Now().Format(time.RFC3339),
		TimeRangeHours: hours,
	}
}

// extractServiceFromEndpoint tries to extract a service name from an endpoint path
// Examples:
//   - "/api/users-service/..." → "users-service"
//   - "/v1/auth/..." → "auth"
//   - "/database/postgres" → "postgres"
func extractServiceFromEndpoint(endpoint string) string {
	parts := strings.Split(strings.Trim(endpoint, "/"), "/")

	// Look for common service patterns
	for _, part := range parts {
		// Skip common prefixes
		if part == "api" || part == "v1" || part == "v2" {
			continue
		}

		// Look for service-like names (contains "service" or database names)
		if strings.Contains(part, "-service") ||
			strings.Contains(part, "service-") ||
			isDatabaseName(part) {
			return part
		}

		// Return first meaningful part
		if len(part) > 2 {
			return part
		}
	}

	return ""
}

// detectServiceType detects whether a service is a database, external service, or internal service
func detectServiceType(serviceName string) string {
	name := strings.ToLower(serviceName)

	if isDatabaseName(name) {
		return "database"
	}

	// Check for external services
	if strings.Contains(name, "external") ||
		strings.Contains(name, "third-party") ||
		strings.Contains(name, "api.") {
		return "external"
	}

	return "service"
}

// isDatabaseName checks if a name looks like a database
func isDatabaseName(name string) bool {
	databases := []string{
		"postgres", "postgresql", "mysql", "mongodb", "mongo", "redis",
		"cassandra", "elasticsearch", "kafka", "rabbitmq", "dynamodb",
		"db", "database", "cache", "queue",
	}

	name = strings.ToLower(name)
	for _, db := range databases {
		if strings.Contains(name, db) {
			return true
		}
	}
	return false
}
