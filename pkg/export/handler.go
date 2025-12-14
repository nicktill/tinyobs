package export

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/config"
	"github.com/nicktill/tinyobs/pkg/httpx"
	"github.com/nicktill/tinyobs/pkg/storage"
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
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
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
		httpx.RespondErrorString(w, http.StatusBadRequest, "Invalid format. Must be 'json' or 'csv'")
		return
	}

	// Parse time range
	end := parseTimeParam(query.Get("end"), time.Now())
	start := parseTimeParam(query.Get("start"), end.Add(-config.DefaultExportWindow))

	// Validate time range
	if !start.Before(end) {
		httpx.RespondErrorString(w, http.StatusBadRequest, "start must be before end")
		return
	}

	// Check if time range is too large
	if end.Sub(start) > config.MaxExportWindow {
		httpx.RespondErrorString(w, http.StatusBadRequest, fmt.Sprintf("Time range too large. Maximum is %v", config.MaxExportWindow))
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
		log.Printf("Export failed: %v", err)
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("export failed: %w", err))
		return
	}

	log.Printf("Exported %d metrics (%s) from %s", result.MetricsExported, format, result.TimeRange)
}

// HandleImport handles POST /v1/import
// Accepts JSON backup files and imports metrics into storage
func (h *Handler) HandleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.RespondErrorString(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		httpx.RespondErrorString(w, http.StatusBadRequest, "Content-Type must be application/json")
		return
	}

	// Import metrics
	ctx := r.Context()
	result, err := h.importer.ImportFromJSON(ctx, r.Body)
	if err != nil {
		log.Printf("Import failed: %v", err)
		httpx.RespondError(w, http.StatusInternalServerError, fmt.Errorf("import failed: %w", err))
		return
	}

	// Log warnings if there were validation errors
	if len(result.Errors) > 0 {
		log.Printf("Import completed with %d validation errors", len(result.Errors))
		for i, err := range result.Errors {
			if i < 10 { // Log first 10 errors
				log.Printf("   - %s", err)
			}
		}
		if len(result.Errors) > 10 {
			log.Printf("   ... and %d more errors", len(result.Errors)-10)
		}
	}

	log.Printf("Imported %d metrics in %d batches from %s", result.MetricsImported, result.BatchesWritten, result.TimeRange)

	// Return result
	httpx.RespondJSON(w, http.StatusOK, result)
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
