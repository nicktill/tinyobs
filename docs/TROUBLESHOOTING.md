# TinyObs Troubleshooting Guide

Common issues and how to fix them.

---

## Dashboard Issues

### Dashboard not loading / 404 error

**Problem:** Visiting `http://localhost:8080/dashboard.html` returns 404 Not Found

**Cause:** The server expects `web/` directory to exist relative to where you run the command

**Solution:**
```bash
# Make sure you run the server from the project root
cd /path/to/tinyobs
go run cmd/server/main.go

# Or build and run from root
go build -o tinyobs cmd/server/main.go
./tinyobs
```

**Alternative:** Use absolute paths by modifying `main.go`:
```go
// Change this:
http.FileServer(http.Dir("./web"))

// To this:
http.FileServer(http.Dir("/absolute/path/to/tinyobs/web"))
```

---

### Dashboard shows "No metrics available"

**Problem:** Dashboard loads but shows no data

**Cause 1:** No metrics have been ingested yet

**Solution:** Start the example app to generate fake metrics:
```bash
go run cmd/example/main.go
```

**Cause 2:** Time range is wrong (querying the future or distant past)

**Solution:** Click "1h" or "6h" to reset the time range to recent data

---

### Charts not updating

**Problem:** Dashboard loads metrics once but doesn't refresh

**Cause:** Browser JavaScript error or polling stopped

**Solution:**
1. Open browser DevTools (F12)
2. Check Console tab for errors
3. Hard refresh: Ctrl+Shift+R (Windows/Linux) or Cmd+Shift+R (Mac)
4. If issue persists, check server logs for errors

---

## Metrics Not Appearing

### Metrics sent but not visible in dashboard

**Problem:** Your app sends metrics but they don't show up

**Cause 1:** Cardinality limit exceeded

**Solution:** Check server logs for "cardinality limit exceeded" errors:
```bash
# Server will log:
WARN: Cardinality limit reached (10000/10000)
```

Fix by either:
- Reducing label cardinality (remove high-cardinality labels like `user_id`, `request_id`)
- Increasing the limit: `export TINYOBS_MAX_CARDINALITY=50000`

**Cause 2:** Metrics sent to wrong endpoint

**Solution:** Verify your SDK configuration:
```go
client, _ := sdk.New(sdk.ClientConfig{
    Service:  "my-app",
    Endpoint: "http://localhost:8080/v1/ingest",  // ← Must be /v1/ingest
})
```

**Cause 3:** Server not running or wrong port

**Solution:**
```bash
# Check if server is running
curl http://localhost:8080/v1/health

# Should return:
{"status":"ok","uptime":"5m42s"}
```

---

### Metrics appear briefly then disappear

**Problem:** Metrics show up initially but vanish after a few minutes

**Cause:** Your timestamps are in the future, and TinyObs retention policy deleted them

**Solution:** Verify your timestamps are correct:
```go
// WRONG: Future timestamp
metrics.Metric{
    Timestamp: time.Now().Add(1 * time.Hour),  // ❌
}

// CORRECT: Current time
metrics.Metric{
    Timestamp: time.Now(),  // ✅
}
```

---

## Performance Issues

### Server using too much memory

**Problem:** TinyObs consuming 2+ GB of RAM

**Cause 1:** Too many series (high cardinality)

**Solution:**
```bash
# Check current cardinality
curl http://localhost:8080/v1/stats | jq .TotalSeries

# If > 10,000:
# 1. Find high-cardinality labels
# 2. Remove dynamic labels (user_id, session_id, request_id)
# 3. Keep only low-cardinality labels (service, endpoint, status)
```

**Cause 2:** In-memory storage mode with lots of data

**Solution:** TinyObs uses BadgerDB by default (persistent). If you modified it to use in-memory storage, switch back to BadgerDB:
```go
// In cmd/server/main.go
store, err := badger.New(badger.Config{Path: "./data"})  // ✅
// NOT: store := memory.New()  // ❌
```

**Cause 3:** No retention policy / compaction not running

**Solution:** Verify compaction is enabled (default: hourly):
```bash
# Check server logs for:
INFO: Compaction completed (raw: 1.2M → 5m: 45K → 1h: 8K)
```

---

### Slow queries (> 1 second)

**Problem:** Queries take 5+ seconds to return

**Cause 1:** Querying too large a time range

**Solution:** Reduce time range or increase `maxPoints`:
```bash
# BAD: 1 year of raw data (millions of points)
curl "http://localhost:8080/v1/query/range?metric=cpu&start=2024-01-01T00:00:00Z&end=2025-01-01T00:00:00Z"

# GOOD: Last 24 hours with max 1000 points
curl "http://localhost:8080/v1/query/range?metric=cpu&start=2024-11-18T00:00:00Z&end=2024-11-19T00:00:00Z&maxPoints=1000"
```

**Cause 2:** Compaction not creating aggregates

**Solution:** Manually trigger compaction:
```bash
# Check if aggregates exist
curl "http://localhost:8080/v1/stats" | jq .

# Restart server to trigger compaction on startup
pkill -f "go run cmd/server/main.go"
go run cmd/server/main.go
```

**Cause 3:** High series count (> 50k)

**Solution:** This is a fundamental scaling limit. Consider:
- Reducing cardinality (fewer labels)
- Using multiple TinyObs instances (one per service)
- Switching to Prometheus/VictoriaMetrics for production scale

---

## Storage Issues

### Disk space filling up

**Problem:** `./data` directory growing too large (> 5 GB)

**Cause 1:** Compaction not running

**Solution:** Check server logs for compaction errors. Common issues:
```bash
# Disk full
ERROR: Compaction failed: no space left on device
# → Free up disk space

# Permission denied
ERROR: Compaction failed: permission denied
# → Fix directory permissions: chmod -R 755 ./data

# BadgerDB corruption
ERROR: Compaction failed: log file corrupted
# → Delete data directory and restart: rm -rf ./data
```

**Cause 2:** Retention policy disabled

**Solution:** Verify retention is enabled:
```bash
# Check server startup logs for:
INFO: Retention enabled (raw: 14d, 5m: 90d, 1h: 1y)
```

**Cause 3:** Too many series (cardinality explosion)

**Solution:** Reduce cardinality (see "Server using too much memory" above)

---

### Data lost after restart

**Problem:** Server restarts and all metrics disappear

**Cause 1:** Using in-memory storage instead of BadgerDB

**Solution:** Verify data directory exists:
```bash
ls -lh ./data
# Should show BadgerDB files

# If empty, you're using in-memory storage
# Check cmd/server/main.go and switch to badger.New()
```

**Cause 2:** Data directory deleted or moved

**Solution:**
```bash
# Check if data directory exists
stat ./data

# If missing, TinyObs creates a new empty database
# Restore from backup if you have one
```

**Cause 3:** BadgerDB corruption

**Solution:**
```bash
# Check server logs for corruption errors:
ERROR: Failed to open storage: manifest has unsupported version

# Recovery options:
# 1. Restore from backup
# 2. Delete corrupted database: rm -rf ./data (loses all data)
# 3. Try BadgerDB recovery tool (advanced)
```

---

## SDK Issues

### SDK not sending metrics

**Problem:** Metrics created in code but never reach server

**Cause 1:** Client not started

**Solution:**
```go
client, _ := sdk.New(sdk.ClientConfig{...})
client.Start(context.Background())  // ← MUST call Start()
defer client.Stop()                 // ← MUST call Stop() on shutdown
```

**Cause 2:** Batching delay (metrics sent every 5 seconds)

**Solution:** This is normal. Wait 5 seconds or manually flush:
```go
client.Flush()  // Force immediate send
```

**Cause 3:** Network error (server unreachable)

**Solution:** Check SDK logs for HTTP errors:
```bash
# Enable verbose logging in SDK
export TINYOBS_DEBUG=1
go run your_app.go

# Look for:
ERROR: Failed to send metrics: dial tcp :8080: connection refused
# → Verify server is running on :8080
```

---

## Common Error Messages

### `cardinality limit exceeded`
**Fix:** Reduce labels or increase `TINYOBS_MAX_CARDINALITY`

### `manifest has unsupported version`
**Fix:** BadgerDB corruption. Delete `./data` and restart.

### `no space left on device`
**Fix:** Free up disk space or reduce retention periods

### `failed to parse time`
**Fix:** Use RFC3339 format: `2024-11-19T10:00:00Z`

### `metric name cannot be empty`
**Fix:** Ensure all metrics have a non-empty name

### `invalid label name`
**Fix:** Label names must match `[a-zA-Z_][a-zA-Z0-9_]*`

---

## Still Having Issues?

1. **Check server logs** - Most errors are logged with clear messages
2. **Enable debug mode** - Set `TINYOBS_DEBUG=1` for verbose output
3. **Verify your setup:**
   ```bash
   # Is server running?
   curl http://localhost:8080/v1/health

   # Can you ingest?
   curl -X POST http://localhost:8080/v1/ingest \
     -H "Content-Type: application/json" \
     -d '{"metrics":[{"name":"test","value":1}]}'

   # Can you query?
   curl "http://localhost:8080/v1/query?metric=test"
   ```
4. **Open an issue** - Include logs, OS, Go version, and steps to reproduce

---

**Pro tip:** Most issues are path-related (web/ directory) or cardinality-related (too many labels). Check those first!
