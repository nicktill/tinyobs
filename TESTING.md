# Testing Guide

## Running Tests

```bash
# Run all tests
go test ./... -v

# Run tests with coverage
go test ./... -cover

# Run specific package tests
go test ./pkg/storage/badger -v
go test ./cmd/server -v
```

## Test Coverage

| Package | Coverage | Notes |
|---------|----------|-------|
| `pkg/sdk/batch` | 95.7% | Excellent coverage of batching logic |
| `pkg/sdk/transport` | 90.0% | HTTP transport well-tested |
| `pkg/storage/memory` | 90.2% | In-memory storage tested |
| `pkg/compaction` | 80.4% | Compaction logic covered |
| `pkg/sdk` | 69.1% | Core SDK functionality |
| `pkg/export` | 50.3% | Export/import tested |
| `pkg/query` | 41.6% | Parser and executor |
| `pkg/ingest` | 34.2% | Ingestion handlers |
| `pkg/tracing` | 38.0% | Distributed tracing |

## Test Structure

### Integration Tests (`cmd/server/integration_test.go`)

Comprehensive end-to-end tests covering:

1. **TestE2E_IngestAndQuery**: Full ingest → query flow
2. **TestE2E_Stats**: Storage statistics endpoint
3. **TestE2E_CompactionWithBadger**: Compaction with persistent storage
4. **TestE2E_InvalidRequests**: Error handling validation
5. **TestE2E_FullPipeline**: Complete pipeline with 1000 metrics
   - Write metrics
   - Run compaction
   - Query downsampled data
   - Restart server (persistence test)
   - Verify data integrity

### Unit Tests

Each package has focused unit tests:

- **Storage Tests** (`pkg/storage/badger/badger_test.go`): Write, query, delete, stats, persistence, compression
- **Parser Tests** (`pkg/query/parser_test.go`): Lexer, vector selectors, range selectors, functions, aggregations
- **Batch Tests** (`pkg/sdk/batch/batch_test.go`): Batching, flushing, concurrency
- **HTTP Tests** (`pkg/sdk/httpx/middleware_test.go`): Auto-instrumentation middleware

## Environment Issues

### Dependency Download Failures

If tests fail with DNS lookup errors like:
```
dial tcp: lookup storage.googleapis.com on [::1]:53: read udp [::1]:37558->[::1]:53: read: connection refused
```

This is a **network environment issue**, not a code problem. The tests are trying to download the `github.com/klauspost/compress` dependency but can't reach Google's Go module proxy.

**Solutions:**

1. **Pre-download dependencies:**
   ```bash
   go mod download
   ```

2. **Use vendor directory:**
   ```bash
   go mod vendor
   go test -mod=vendor ./...
   ```

3. **Use cached modules** (if available):
   ```bash
   GOPROXY=off go test ./...
   ```

4. **On normal networks:** Tests will work fine with internet access

## Test Quality Notes

✅ **Strengths:**
- Comprehensive integration tests covering real-world scenarios
- Persistence testing (restart simulation)
- Concurrent operation testing
- Large-scale testing (1000-10000 metrics)
- Error case coverage

⚠️ **Improvement Areas:**
- WebSocket handler tests needed
- Dashboard API endpoint tests
- Trace propagation tests
- Query executor edge cases
- Runtime metrics collector tests

## Continuous Integration

For CI/CD pipelines, ensure:

1. Dependencies are pre-downloaded or cached
2. `go.sum` is committed to repo
3. Tests run with `-race` flag for race detection:
   ```bash
   go test -race ./...
   ```

4. Coverage reports generated:
   ```bash
   go test -coverprofile=coverage.out ./...
   go tool cover -html=coverage.out -o coverage.html
   ```

## Writing New Tests

Follow these patterns:

```go
func TestFeature_Behavior(t *testing.T) {
    // Setup
    store, err := badger.New(badger.Config{InMemory: true})
    if err != nil {
        t.Fatalf("Setup failed: %v", err)
    }
    defer store.Close()

    ctx := context.Background()

    // Execute
    result, err := feature.DoSomething(ctx, input)
    if err != nil {
        t.Fatalf("Operation failed: %v", err)
    }

    // Verify
    if result != expected {
        t.Errorf("Expected %v, got %v", expected, result)
    }
}
```

## Performance Benchmarks

Run benchmarks with:

```bash
go test -bench=. -benchmem ./pkg/storage/badger
```

Example output:
```
BenchmarkWrite-8     20000   52340 ns/op   2048 B/op   15 allocs/op
BenchmarkQuery-8     10000  120450 ns/op   4096 B/op   32 allocs/op
```
