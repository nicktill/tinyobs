package export

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/storage"
)

const (
	// DefaultExportWindow is the default time range for exports (last 24 hours)
	DefaultExportWindow = 24 * time.Hour

	// MaxExportWindow is the maximum allowed export time range (30 days)
	MaxExportWindow = 30 * 24 * time.Hour
)

// Handler handles export/import HTTP endpoints
type Handler struct {
	exporter *Exporter
	importer *Importer
}

// NewHandler creates a new export/import handler
func NewHandler(store storage.Storage) *Handler {
	return &Handler{
		exporter: NewExporter(store),
		importer: NewImporter(store),
	}
}

// HandleExport handles GET /v1/export
// Query params:
//   - format: "json" or "csv" (default: json)
//   - start: RFC3339 timestamp (default: 24h ago)
//   - end: RFC3339 timestamp (default: now)
//   - metric: metric name filter (optional)
func (h *Handler) HandleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Parse format (default: json)
	format := query.Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		http.Error(w, "Invalid format. Must be 'json' or 'csv'", http.StatusBadRequest)
		return
	}

	// Parse time range
	end := parseTimeParam(query.Get("end"), time.Now())
	start := parseTimeParam(query.Get("start"), end.Add(-DefaultExportWindow))

	// Validate time range
	if !start.Before(end) {
		http.Error(w, "start must be before end", http.StatusBadRequest)
		return
	}

	// Check if time range is too large
	if end.Sub(start) > MaxExportWindow {
		http.Error(w, fmt.Sprintf("Time range too large. Maximum is %v", MaxExportWindow), http.StatusBadRequest)
		return
	}

	// Build export options
	opts := ExportOptions{
		Start:  start,
		End:    end,
		Format: format,
	}

	// Optional metric name filter
	if metricName := query.Get("metric"); metricName != "" {
		opts.MetricNames = []string{metricName}
	}

	// Set appropriate headers
	timestamp := time.Now().Format("20060102-150405")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=tinyobs-export-%s.json", timestamp))
	} else {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=tinyobs-export-%s.csv", timestamp))
	}

	// Export metrics
	ctx := r.Context()
	var result *ExportResult
	var err error

	if format == "json" {
		result, err = h.exporter.ExportToJSON(ctx, w, opts)
	} else {
		result, err = h.exporter.ExportToCSV(ctx, w, opts)
	}

	if err != nil {
		log.Printf("❌ Export failed: %v", err)
		http.Error(w, fmt.Sprintf("Export failed: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Exported %d metrics (%s) from %s", result.MetricsExported, format, result.TimeRange)
}

// HandleImport handles POST /v1/import
// Accepts JSON backup files and imports metrics into storage
func (h *Handler) HandleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusBadRequest)
		return
	}

	// Import metrics
	ctx := r.Context()
	result, err := h.importer.ImportFromJSON(ctx, r.Body)
	if err != nil {
		log.Printf("❌ Import failed: %v", err)
		http.Error(w, fmt.Sprintf("Import failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Log warnings if there were validation errors
	if len(result.Errors) > 0 {
		log.Printf("⚠️  Import completed with %d validation errors", len(result.Errors))
		for i, err := range result.Errors {
			if i < 10 { // Log first 10 errors
				log.Printf("   - %s", err)
			}
		}
		if len(result.Errors) > 10 {
			log.Printf("   ... and %d more errors", len(result.Errors)-10)
		}
	}

	log.Printf("✅ Imported %d metrics in %d batches from %s", result.MetricsImported, result.BatchesWritten, result.TimeRange)

	// Return result
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("❌ Failed to encode import response: %v", err)
	}
}

// parseTimeParam parses a time parameter or returns default
func parseTimeParam(param string, defaultTime time.Time) time.Time {
	if param == "" {
		return defaultTime
	}

	// Try RFC3339 format
	if t, err := time.Parse(time.RFC3339, param); err == nil {
		return t
	}

	// Try simple datetime format
	if t, err := time.Parse("2006-01-02T15:04:05", param); err == nil {
		return t
	}

	// Return default if parsing fails
	return defaultTime
}
