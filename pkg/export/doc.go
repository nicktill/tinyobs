// Package export provides metrics backup and restore functionality.
//
// # Overview
//
// The export package enables users to backup their TinyObs metrics to JSON or CSV
// files and restore them later. This is useful for:
//   - Data backup and disaster recovery
//   - Migrating metrics between TinyObs instances
//   - Exporting data for analysis in external tools
//   - Archiving historical metrics
//
// # Supported Formats
//
// JSON Format:
//   - Preserves all metric metadata (name, type, labels, timestamp, value)
//   - Includes export metadata (timestamp, time range, metric count)
//   - Can be re-imported into TinyObs
//   - Human-readable with pretty-printing
//
// CSV Format:
//   - Flattened representation suitable for spreadsheets
//   - Dynamic columns based on label keys present in data
//   - Good for analysis in Excel, pandas, or other tools
//   - Cannot be re-imported (export-only)
//
// # HTTP API
//
// Export endpoint: GET /v1/export
// Query parameters:
//   - format: "json" or "csv" (default: json)
//   - start: RFC3339 timestamp (default: 24h ago)
//   - end: RFC3339 timestamp (default: now)
//   - metric: metric name filter (optional)
//
// Example:
//
//	curl "http://localhost:8080/v1/export?format=json&start=2025-11-18T00:00:00Z" \
//	  -o backup.json
//
// Import endpoint: POST /v1/import
// Content-Type: application/json
//
// Example:
//
//	curl -X POST "http://localhost:8080/v1/import" \
//	  -H "Content-Type: application/json" \
//	  -d @backup.json
//
// # Usage Limits
//
//   - Maximum export time range: 30 days
//   - Default export window: 24 hours
//   - Import batch size: 5,000 metrics per write operation
//   - Validation: Metrics older than 10 years or 1 day in future are rejected
//
// # Programmatic Usage
//
// Exporting metrics:
//
//	exporter := export.NewExporter(storage)
//	opts := export.ExportOptions{
//	    Start:  time.Now().Add(-24 * time.Hour),
//	    End:    time.Now(),
//	    Format: "json",
//	}
//
//	file, _ := os.Create("backup.json")
//	defer file.Close()
//
//	result, err := exporter.ExportToJSON(ctx, file, opts)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Exported %d metrics\n", result.MetricsExported)
//
// Importing metrics:
//
//	importer := export.NewImporter(storage)
//
//	file, _ := os.Open("backup.json")
//	defer file.Close()
//
//	result, err := importer.ImportFromJSON(ctx, file)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Imported %d metrics in %d batches\n",
//	    result.MetricsImported, result.BatchesWritten)
//
// # Data Format
//
// The JSON export format includes metadata and metrics:
//
//	{
//	  "metadata": {
//	    "exported_at": "2025-11-19T03:00:00Z",
//	    "start_time": "2025-11-18T03:00:00Z",
//	    "end_time": "2025-11-19T03:00:00Z",
//	    "metric_count": 1000,
//	    "format": "json",
//	    "version": "1.0"
//	  },
//	  "metrics": [
//	    {
//	      "name": "http_requests_total",
//	      "type": "counter",
//	      "value": 42,
//	      "labels": {
//	        "service": "api",
//	        "endpoint": "/users"
//	      },
//	      "timestamp": "2025-11-19T02:30:00Z"
//	    }
//	  ]
//	}
//
// # Error Handling
//
// Import operations validate each metric and skip invalid ones rather than
// failing the entire import. Validation errors are reported in the ImportResult:
//
//	result, err := importer.ImportFromJSON(ctx, file)
//	if err != nil {
//	    // Fatal error - import could not proceed
//	    log.Fatal(err)
//	}
//
//	if len(result.Errors) > 0 {
//	    // Some metrics were invalid and skipped
//	    for _, errMsg := range result.Errors {
//	        log.Printf("Validation error: %s", errMsg)
//	    }
//	}
//
//	log.Printf("Successfully imported %d/%d metrics",
//	    result.MetricsImported,
//	    result.MetricsImported + len(result.Errors))
package export
