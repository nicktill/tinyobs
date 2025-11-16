# Quick Start: Addressing Code Review Findings

This guide helps you quickly address the most critical findings from the code review.

## 🚨 Critical Fixes (Do These First - ~4 hours)

### 1. Fix Path Traversal Vulnerability (15 minutes)

**File:** `cmd/server/main.go`  
**Line:** 79  
**Risk:** HIGH - Allows reading arbitrary files from server

```go
// ❌ Current (vulnerable):
router.PathPrefix("/").Handler(http.FileServer(http.Dir("./web/")))

// ✅ Fixed:
fs := http.FileServer(http.Dir("./web/"))
router.PathPrefix("/").Handler(http.StripPrefix("/", fs))
```

**Test:**
```bash
# This should NOT work after fix:
curl http://localhost:8080/../go.mod
```

---

### 2. Fix Template Execution Error (5 minutes)

**File:** `cmd/example/main.go`  
**Line:** 647  
**Risk:** MEDIUM - Silent failures

```go
// ❌ Current:
tmpl.Execute(w, nil)

// ✅ Fixed:
if err := tmpl.Execute(w, nil); err != nil {
    log.Printf("Template execution failed: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
```

---

### 3. Replace Bubble Sort (5 minutes)

**File:** `pkg/ingest/cardinality.go`  
**Lines:** 136-142  
**Risk:** LOW - Performance issue with many labels

```go
// ❌ Current (O(n²)):
for i := 0; i < len(keys); i++ {
    for j := i + 1; j < len(keys); j++ {
        if keys[i] > keys[j] {
            keys[i], keys[j] = keys[j], keys[i]
        }
    }
}

// ✅ Fixed (O(n log n)):
sort.Strings(keys)
```

---

### 4. Add Authentication Middleware (2 hours)

**File:** Create `pkg/ingest/auth.go`

```go
package ingest

import (
    "crypto/subtle"
    "net/http"
    "os"
    "strings"
)

// AuthMiddleware validates API keys
func AuthMiddleware(next http.Handler) http.Handler {
    // Load API keys from environment
    validKeys := loadAPIKeys()
    
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Skip auth for static files and metrics endpoint
        if strings.HasPrefix(r.URL.Path, "/metrics") || 
           !strings.HasPrefix(r.URL.Path, "/v1/") {
            next.ServeHTTP(w, r)
            return
        }
        
        // Get API key from header
        key := r.Header.Get("X-API-Key")
        if key == "" {
            http.Error(w, "Missing API key", http.StatusUnauthorized)
            return
        }
        
        // Validate API key (constant-time comparison)
        valid := false
        for _, validKey := range validKeys {
            if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) == 1 {
                valid = true
                break
            }
        }
        
        if !valid {
            http.Error(w, "Invalid API key", http.StatusUnauthorized)
            return
        }
        
        next.ServeHTTP(w, r)
    })
}

func loadAPIKeys() []string {
    // Load from environment variable (comma-separated)
    keysStr := os.Getenv("TINYOBS_API_KEYS")
    if keysStr == "" {
        // Default key for development (CHANGE IN PRODUCTION!)
        return []string{"dev-key-change-me"}
    }
    return strings.Split(keysStr, ",")
}
```

**File:** Update `cmd/server/main.go`

```go
// Add after router creation
router := mux.NewRouter()

// Wrap API routes with authentication
api := router.PathPrefix("/v1").Subrouter()
api.Use(ingest.AuthMiddleware)  // Add this line
api.HandleFunc("/ingest", handler.HandleIngest).Methods("POST")
// ... rest of API routes
```

**Configuration:**
```bash
# Set in environment or .env file
export TINYOBS_API_KEYS="your-secret-key-1,your-secret-key-2"
```

---

### 5. Add Rate Limiting (1.5 hours)

**File:** Create `pkg/ingest/ratelimit.go`

```go
package ingest

import (
    "net/http"
    "sync"
    "golang.org/x/time/rate"
)

type RateLimiter struct {
    limiters map[string]*rate.Limiter
    mu       sync.Mutex
    rps      rate.Limit
    burst    int
}

func NewRateLimiter(rps int, burst int) *RateLimiter {
    return &RateLimiter{
        limiters: make(map[string]*rate.Limiter),
        rps:      rate.Limit(rps),
        burst:    burst,
    }
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    limiter, exists := rl.limiters[ip]
    if !exists {
        limiter = rate.NewLimiter(rl.rps, rl.burst)
        rl.limiters[ip] = limiter
    }
    
    return limiter
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ip := r.RemoteAddr
        limiter := rl.getLimiter(ip)
        
        if !limiter.Allow() {
            http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
            return
        }
        
        next.ServeHTTP(w, r)
    })
}
```

**File:** Update `cmd/server/main.go`

```go
// Add rate limiter
rateLimiter := ingest.NewRateLimiter(100, 200) // 100 req/s, burst 200

// Wrap API routes
api := router.PathPrefix("/v1").Subrouter()
api.Use(rateLimiter.Middleware)  // Add this line
api.Use(ingest.AuthMiddleware)
// ... rest of routes
```

**Install dependency:**
```bash
go get golang.org/x/time/rate
```

---

## ⚡ High Priority (Next - ~1 day)

### 6. Add HTTP Handler Tests

**File:** Create `pkg/ingest/handler_test.go`

```go
package ingest

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "tinyobs/pkg/sdk/metrics"
    "tinyobs/pkg/storage/memory"
)

func TestHandleIngest(t *testing.T) {
    store := memory.New()
    handler := NewHandler(store)
    
    tests := []struct {
        name       string
        request    IngestRequest
        wantStatus int
        wantError  bool
    }{
        {
            name: "valid metrics",
            request: IngestRequest{
                Metrics: []metrics.Metric{
                    {
                        Name:  "test_metric",
                        Value: 42.0,
                        Labels: map[string]string{
                            "env": "test",
                        },
                    },
                },
            },
            wantStatus: http.StatusOK,
            wantError:  false,
        },
        {
            name: "empty metric name",
            request: IngestRequest{
                Metrics: []metrics.Metric{
                    {
                        Name:  "",
                        Value: 42.0,
                    },
                },
            },
            wantStatus: http.StatusBadRequest,
            wantError:  true,
        },
        {
            name: "too many metrics",
            request: IngestRequest{
                Metrics: make([]metrics.Metric, 1001), // Over limit
            },
            wantStatus: http.StatusBadRequest,
            wantError:  true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            body, _ := json.Marshal(tt.request)
            req := httptest.NewRequest("POST", "/v1/ingest", bytes.NewReader(body))
            w := httptest.NewRecorder()
            
            handler.HandleIngest(w, req)
            
            if w.Code != tt.wantStatus {
                t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
            }
        })
    }
}
```

**Run tests:**
```bash
go test ./pkg/ingest -v
```

---

### 7. Add Structured Logging

**Install slog (already in Go 1.21+):**
```go
import "log/slog"
```

**File:** Update `cmd/server/main.go`

```go
// Replace log package with slog
import (
    "log/slog"
    "os"
)

func main() {
    // Create structured logger
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    }))
    slog.SetDefault(logger)
    
    // Use structured logging
    slog.Info("server starting",
        "port", 8080,
        "storage", "badger",
        "data_dir", dataDir)
    
    // ... rest of code
}
```

**Benefits:**
- Structured logs for better searching
- JSON format for log aggregation
- Log levels for filtering
- Contextual information

---

## 📊 Quick Wins (30 minutes total)

### 8. Check All JSON Encoding Errors

**Search and replace pattern:**
```go
// Find:
json.NewEncoder(w).Encode(

// Replace with:
if err := json.NewEncoder(w).Encode(

// Then add after each:
); err != nil {
    slog.Error("json encoding failed", "error", err)
}
```

**Files to update:**
- `pkg/ingest/handler.go` (multiple locations)
- `pkg/ingest/dashboard.go` (multiple locations)

---

### 9. Add .env File Support

**File:** Create `.env.example`

```bash
# TinyObs Configuration
TINYOBS_PORT=8080
TINYOBS_DATA_DIR=./data/tinyobs
TINYOBS_API_KEYS=your-secret-key-here
TINYOBS_ENABLE_AUTH=true
TINYOBS_RATE_LIMIT_RPS=100
TINYOBS_RATE_LIMIT_BURST=200
```

**Install godotenv:**
```bash
go get github.com/joho/godotenv
```

**File:** Update `cmd/server/main.go`

```go
import "github.com/joho/godotenv"

func main() {
    // Load .env file if it exists
    _ = godotenv.Load()
    
    // Read from environment
    port := os.Getenv("TINYOBS_PORT")
    if port == "" {
        port = "8080"
    }
    
    // ... rest of code
}
```

---

## 🧪 Testing Checklist

```bash
# 1. Run all tests
go test ./... -v

# 2. Run with race detector
go test ./... -race

# 3. Run go vet
go vet ./...

# 4. Build to ensure no errors
go build ./...

# 5. Test the server
# Terminal 1:
go run cmd/server/main.go

# Terminal 2:
curl -H "X-API-Key: dev-key-change-me" \
  -X POST http://localhost:8080/v1/ingest \
  -d '{"metrics":[{"name":"test","value":42}]}'

# Should return: {"status":"success","count":1}
```

---

## 📚 Documentation Updates

### Create CONTRIBUTING.md

```markdown
# Contributing to TinyObs

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/tinyobs.git`
3. Install dependencies: `go mod download`
4. Run tests: `go test ./...`

## Making Changes

1. Create a branch: `git checkout -b feature/my-feature`
2. Make your changes
3. Run tests: `go test ./...`
4. Run linters: `go vet ./...`
5. Commit: `git commit -m "Add my feature"`
6. Push: `git push origin feature/my-feature`
7. Open a Pull Request

## Code Style

- Follow standard Go conventions
- Run `go fmt` before committing
- Add tests for new features
- Update documentation

## Running Locally

```bash
# Start server
go run cmd/server/main.go

# Start example app
go run cmd/example/main.go
```

## Questions?

Open an issue or reach out to the maintainers.
```

---

## ⏱️ Time Estimates

| Task | Time | Priority |
|------|------|----------|
| Fix path traversal | 15 min | Critical |
| Fix template error | 5 min | Critical |
| Replace bubble sort | 5 min | Critical |
| Add authentication | 2 hours | Critical |
| Add rate limiting | 1.5 hours | Critical |
| Add HTTP tests | 2 hours | High |
| Add structured logging | 1 hour | High |
| Fix JSON encoding | 30 min | High |
| Add .env support | 30 min | Medium |
| Create CONTRIBUTING.md | 30 min | Medium |

**Total Critical:** ~4 hours  
**Total High Priority:** ~4 hours  
**Total Medium Priority:** ~1 hour

**Full completion:** ~9 hours (approximately 1 full day)

---

## 🎯 Validation Checklist

After making changes:

- [ ] All tests pass: `go test ./...`
- [ ] No race conditions: `go test ./... -race`
- [ ] Code quality: `go vet ./...`
- [ ] Build succeeds: `go build ./...`
- [ ] Server starts without errors
- [ ] Authentication works correctly
- [ ] Rate limiting works correctly
- [ ] Documentation updated
- [ ] README reflects changes

---

## 📞 Need Help?

1. Check the comprehensive `CODE_REVIEW_REPORT.md` for detailed explanations
2. Review `IMPROVEMENTS_CHECKLIST.md` for all recommendations
3. Read `SECURITY.md` for security guidelines
4. See `EXECUTIVE_SUMMARY.md` for high-level overview

---

**Created:** November 16, 2025  
**Last Updated:** November 16, 2025  
**For:** TinyObs v2.1 Pre-release Review
