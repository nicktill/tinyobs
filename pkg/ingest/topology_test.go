package ingest

import (
	"testing"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

func TestBuildTopology(t *testing.T) {
	// Create sample metrics simulating service-to-service communication
	now := time.Now()
	testMetrics := []metrics.Metric{
		{
			Name:  "http_requests_total",
			Type:  metrics.CounterType,
			Value: 100,
			Labels: map[string]string{
				"service":  "api-gateway",
				"endpoint": "/api/users-service/list",
			},
			Timestamp: now,
		},
		{
			Name:  "http_requests_total",
			Type:  metrics.CounterType,
			Value: 50,
			Labels: map[string]string{
				"service":        "api-gateway",
				"target_service": "auth-service",
			},
			Timestamp: now,
		},
		{
			Name:  "http_requests_total",
			Type:  metrics.CounterType,
			Value: 75,
			Labels: map[string]string{
				"service": "users-service",
				"upstream": "postgres",
			},
			Timestamp: now,
		},
		{
			Name:  "errors_total",
			Type:  metrics.CounterType,
			Value: 5,
			Labels: map[string]string{
				"service": "api-gateway",
			},
			Timestamp: now,
		},
	}

	timeRange := 1 * time.Hour
	topology := buildTopology(testMetrics, timeRange)

	// Verify nodes were created
	if len(topology.Nodes) == 0 {
		t.Fatal("Expected nodes to be created")
	}

	// Find api-gateway node
	var apiGateway *TopologyNode
	for i := range topology.Nodes {
		if topology.Nodes[i].ID == "api-gateway" {
			apiGateway = &topology.Nodes[i]
			break
		}
	}

	if apiGateway == nil {
		t.Fatal("Expected api-gateway node to exist")
	}

	// Verify request rate calculation (per hour: 100 + 50 = 150)
	expectedRate := 150.0 / timeRange.Hours()
	if apiGateway.RequestRate != expectedRate {
		t.Errorf("Expected request rate %.2f, got %.2f", expectedRate, apiGateway.RequestRate)
	}

	// Verify edges were created
	if len(topology.Edges) == 0 {
		t.Fatal("Expected edges to be created")
	}

	// Check for edge from api-gateway to users-service
	foundUsersEdge := false
	foundAuthEdge := false
	for _, edge := range topology.Edges {
		if edge.Source == "api-gateway" && edge.Target == "users-service" {
			foundUsersEdge = true
		}
		if edge.Source == "api-gateway" && edge.Target == "auth-service" {
			foundAuthEdge = true
		}
	}

	if !foundUsersEdge {
		t.Error("Expected edge from api-gateway to users-service")
	}
	if !foundAuthEdge {
		t.Error("Expected edge from api-gateway to auth-service")
	}

	// Verify time range is set
	if topology.TimeRangeHours != 1.0 {
		t.Errorf("Expected time range 1.0 hours, got %.2f", topology.TimeRangeHours)
	}
}

func TestExtractServiceFromEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		expected string
	}{
		{"/api/users-service/list", "users-service"},
		{"/v1/auth-service/login", "auth-service"},
		{"/api/v2/payment-service/process", "payment-service"},
		{"/database/postgres/query", "database"}, // "database" is detected, not "postgres"
		{"/api/v1/short", "short"},
		{"/a/b/c", ""}, // Short path segments are skipped
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			result := extractServiceFromEndpoint(tt.endpoint)
			if result != tt.expected {
				t.Errorf("extractServiceFromEndpoint(%q) = %q, want %q", tt.endpoint, result, tt.expected)
			}
		})
	}
}

func TestDetectServiceType(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"postgres", "database"},
		{"my-postgres-db", "database"},
		{"redis-cache", "database"},
		{"mongodb", "database"},
		{"api-service", "service"},
		{"external-api", "external"},
		{"third-party-service", "external"},
		{"user-service", "service"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectServiceType(tt.name)
			if result != tt.expected {
				t.Errorf("detectServiceType(%q) = %q, want %q", tt.name, result, tt.expected)
			}
		})
	}
}
