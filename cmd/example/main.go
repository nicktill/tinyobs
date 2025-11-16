package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"tinyobs/pkg/sdk"
	"tinyobs/pkg/sdk/httpx"
)

var (
	requestCount   int64
	errorCount     int64
	activeRequests int64
	startTime      time.Time
)

func main() {
	startTime = time.Now()

	// Initialize TinyObs client
	log.Println("🚀 Initializing TinyObs client...")
	client, err := sdk.New(sdk.ClientConfig{
		Service:    "example-app",
		APIKey:     "demo-key",
		Endpoint:   "http://localhost:8080/v1/ingest",
		FlushEvery: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("❌ Failed to create TinyObs client: %v", err)
	}

	// Start the client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("📊 Starting metrics collection...")
	if err := client.Start(ctx); err != nil {
		log.Fatalf("❌ Failed to start TinyObs client: %v", err)
	}
	defer client.Stop()

	// Create metrics
	requestCounter := client.Counter("http_requests_total")
	requestDuration := client.Histogram("http_request_duration_seconds")
	activeUsers := client.Gauge("active_users")
	errorCounter := client.Counter("errors_total")

	// Create HTTP server with middleware
	mux := http.NewServeMux()

	// Add TinyObs middleware
	handler := httpx.Middleware(client)(mux)

	// Stats page with live dashboard
	mux.HandleFunc("/", serveStatsPage)

	// Example endpoints
	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&activeRequests, 1)
		defer atomic.AddInt64(&activeRequests, -1)

		start := time.Now()
		latency := time.Duration(rand.Intn(200)) * time.Millisecond
		time.Sleep(latency)

		// Randomly simulate errors
		if rand.Float32() < 0.1 { // 10% error rate
			atomic.AddInt64(&errorCount, 1)
			errorCounter.Inc("type", "api_error", "endpoint", "/api/users")
			log.Printf("⚠️  ERROR: /api/users request failed (latency: %v)", latency)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		atomic.AddInt64(&requestCount, 1)
		requestCounter.Inc("endpoint", "/api/users", "method", r.Method)
		requestDuration.Observe(time.Since(start).Seconds())

		log.Printf("✅ /api/users - 200 OK (latency: %v)", latency)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"users": [{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}]}`)
	})

	mux.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&activeRequests, 1)
		defer atomic.AddInt64(&activeRequests, -1)

		start := time.Now()
		latency := time.Duration(rand.Intn(300)) * time.Millisecond
		time.Sleep(latency)

		atomic.AddInt64(&requestCount, 1)
		requestCounter.Inc("endpoint", "/api/orders", "method", r.Method)
		requestDuration.Observe(time.Since(start).Seconds())

		log.Printf("✅ /api/orders - 200 OK (latency: %v)", latency)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"orders": [{"id": 1, "total": 99.99}, {"id": 2, "total": 149.99}]}`)
	})

	mux.HandleFunc("/api/products", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&activeRequests, 1)
		defer atomic.AddInt64(&activeRequests, -1)

		start := time.Now()
		latency := time.Duration(rand.Intn(150)) * time.Millisecond
		time.Sleep(latency)

		atomic.AddInt64(&requestCount, 1)
		requestCounter.Inc("endpoint", "/api/products", "method", r.Method)
		requestDuration.Observe(time.Since(start).Seconds())

		log.Printf("✅ /api/products - 200 OK (latency: %v)", latency)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"products": [{"id": 1, "name": "Widget"}, {"id": 2, "name": "Gadget"}]}`)
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status": "healthy", "uptime": "%v", "timestamp": "%s"}`,
			time.Since(startTime).Round(time.Second), time.Now().Format(time.RFC3339))
	})

	// Stats API
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"requests": %d,
			"errors": %d,
			"active": %d,
			"uptime": "%v"
		}`, atomic.LoadInt64(&requestCount), atomic.LoadInt64(&errorCount),
			atomic.LoadInt64(&activeRequests), time.Since(startTime).Round(time.Second))
	})

	// Start server
	server := &http.Server{
		Addr:    ":3001",
		Handler: handler,
	}

	go func() {
		log.Println("🌐 Starting example app on http://localhost:3001")
		log.Println("📊 TinyObs dashboard: http://localhost:8080/dashboard.html")
		log.Println("📈 Generating simulated traffic...")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ Server failed to start: %v", err)
		}
	}()

	// Simulate background activity
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		log.Println("⚙️  Background job started (runs every 2s)")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				latency := rand.Float64() * 0.5
				requestCounter.Inc("type", "background_job")
				requestDuration.Observe(latency)
				activeUsers.Set(float64(rand.Intn(10) + 1))
				log.Printf("🔄 Background job executed (latency: %.3fs, active_users: %d)", latency, int(rand.Intn(10)+1))
			}
		}
	}()

	// Simulate periodic traffic
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		endpoints := []string{"/api/users", "/api/orders", "/api/products"}
		log.Println("🚦 Traffic simulator started (requests every 5s)")

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				endpoint := endpoints[rand.Intn(len(endpoints))]
				go func(ep string) {
					resp, err := http.Get("http://localhost:3001" + ep)
					if err != nil {
						log.Printf("⚠️  Traffic simulation failed for %s: %v", ep, err)
						return
					}
					defer resp.Body.Close()
					log.Printf("🔵 Simulated request to %s - %d %s", ep, resp.StatusCode, resp.Status)
				}(endpoint)
			}
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down example app...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("❌ Server forced to shutdown: %v", err)
	}

	log.Println("👋 Example app exited")
}

var statsPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>TinyObs Example App</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            min-height: 100vh;
            padding: 2rem;
        }
        .container {
            max-width: 1200px;
            margin: 0 auto;
        }
        .header {
            text-align: center;
            margin-bottom: 3rem;
        }
        .header h1 {
            font-size: 3rem;
            margin-bottom: 0.5rem;
        }
        .header p {
            font-size: 1.25rem;
            opacity: 0.9;
        }
        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 1.5rem;
            margin-bottom: 2rem;
        }
        .stat-card {
            background: rgba(255, 255, 255, 0.1);
            backdrop-filter: blur(10px);
            border-radius: 12px;
            padding: 2rem;
            border: 1px solid rgba(255, 255, 255, 0.2);
        }
        .stat-label {
            font-size: 0.875rem;
            opacity: 0.8;
            text-transform: uppercase;
            letter-spacing: 1px;
            margin-bottom: 0.5rem;
        }
        .stat-value {
            font-size: 3rem;
            font-weight: bold;
        }
        .endpoints {
            background: rgba(255, 255, 255, 0.1);
            backdrop-filter: blur(10px);
            border-radius: 12px;
            padding: 2rem;
            border: 1px solid rgba(255, 255, 255, 0.2);
        }
        .endpoint {
            display: flex;
            justify-content: space-between;
            padding: 1rem;
            margin: 0.5rem 0;
            background: rgba(255, 255, 255, 0.05);
            border-radius: 8px;
            border: 1px solid rgba(255, 255, 255, 0.1);
        }
        .endpoint:hover {
            background: rgba(255, 255, 255, 0.1);
        }
        .endpoint-path {
            font-family: 'Monaco', monospace;
            font-weight: 600;
        }
        .btn {
            background: rgba(255, 255, 255, 0.2);
            border: 1px solid rgba(255, 255, 255, 0.3);
            color: white;
            padding: 0.5rem 1rem;
            border-radius: 6px;
            cursor: pointer;
            text-decoration: none;
            display: inline-block;
            transition: all 0.2s;
        }
        .btn:hover {
            background: rgba(255, 255, 255, 0.3);
        }
        .log-container {
            background: rgba(0, 0, 0, 0.3);
            border-radius: 12px;
            padding: 1.5rem;
            margin-top: 2rem;
            max-height: 300px;
            overflow-y: auto;
            font-family: 'Monaco', monospace;
            font-size: 0.875rem;
        }
        .log-entry {
            padding: 0.5rem 0;
            border-bottom: 1px solid rgba(255, 255, 255, 0.1);
        }
        .log-time {
            opacity: 0.6;
            margin-right: 1rem;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚀 TinyObs Example App</h1>
            <p>Simulated traffic generator with live metrics</p>
        </div>

        <div class="stats-grid">
            <div class="stat-card">
                <div class="stat-label">Total Requests</div>
                <div class="stat-value" id="requests">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Errors</div>
                <div class="stat-value" id="errors">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Active Requests</div>
                <div class="stat-value" id="active">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Uptime</div>
                <div class="stat-value" id="uptime" style="font-size: 2rem;">0s</div>
            </div>
        </div>

        <div class="endpoints">
            <h2 style="margin-bottom: 1.5rem;">📡 Available Endpoints</h2>
            <div class="endpoint">
                <span class="endpoint-path">GET /api/users</span>
                <a href="/api/users" class="btn" target="_blank">Test</a>
            </div>
            <div class="endpoint">
                <span class="endpoint-path">GET /api/orders</span>
                <a href="/api/orders" class="btn" target="_blank">Test</a>
            </div>
            <div class="endpoint">
                <span class="endpoint-path">GET /api/products</span>
                <a href="/api/products" class="btn" target="_blank">Test</a>
            </div>
            <div class="endpoint">
                <span class="endpoint-path">GET /health</span>
                <a href="/health" class="btn" target="_blank">Test</a>
            </div>
            <div style="margin-top: 1.5rem; text-align: center;">
                <a href="http://localhost:8080/dashboard.html" class="btn" style="padding: 1rem 2rem; font-size: 1.125rem;">
                    📊 Open TinyObs Dashboard
                </a>
            </div>
        </div>

        <div class="log-container">
            <h3 style="margin-bottom: 1rem;">📋 Recent Activity</h3>
            <div id="logs"></div>
        </div>
    </div>

    <script>
        const logs = [];
        const maxLogs = 20;

        function addLog(message) {
            const time = new Date().toLocaleTimeString();
            logs.unshift({ time, message });
            if (logs.length > maxLogs) logs.pop();

            const container = document.getElementById('logs');
            container.innerHTML = logs.map(log =>
                '<div class="log-entry"><span class="log-time">' + log.time + '</span>' + log.message + '</div>'
            ).join('');
        }

        async function updateStats() {
            try {
                const response = await fetch('/api/stats');
                const data = await response.json();

                document.getElementById('requests').textContent = data.requests.toLocaleString();
                document.getElementById('errors').textContent = data.errors.toLocaleString();
                document.getElementById('active').textContent = data.active;
                document.getElementById('uptime').textContent = data.uptime;
            } catch (error) {
                console.error('Failed to fetch stats:', error);
            }
        }

        // Initial load
        updateStats();
        addLog('🚀 App started - monitoring metrics');
        addLog('📊 Sending metrics to TinyObs every 5s');
        addLog('🔄 Background jobs running every 2s');
        addLog('🚦 Simulated traffic running every 5s');

        // Update every second
        setInterval(updateStats, 1000);

        // Add activity log every 3 seconds
        setInterval(() => {
            const activities = [
                '✅ Metrics batch sent to TinyObs',
                '🔄 Background job completed',
                '📈 Active users gauge updated',
                '⚡ Request processed successfully',
                '🎯 Counter metrics incremented'
            ];
            addLog(activities[Math.floor(Math.random() * activities.length)]);
        }, 3000);
    </script>
</body>
</html>
`

func serveStatsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	tmpl := template.Must(template.New("stats").Parse(statsPageTemplate))
	tmpl.Execute(w, nil)
}
