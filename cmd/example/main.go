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

	"github.com/nicktill/tinyobs/pkg/sdk"
	"github.com/nicktill/tinyobs/pkg/sdk/httpx"
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

        :root {
            --bg-primary: #0d1117;
            --bg-secondary: #161b22;
            --bg-tertiary: #21262d;
            --border-color: #30363d;
            --text-primary: #c9d1d9;
            --text-secondary: #8b949e;
            --accent-blue: #58a6ff;
            --accent-green: #3fb950;
            --accent-orange: #f0883e;
            --accent-red: #f85149;
        }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans', Helvetica, Arial, sans-serif;
            background: var(--bg-primary);
            color: var(--text-primary);
            line-height: 1.6;
            min-height: 100vh;
        }

        .header {
            background: var(--bg-secondary);
            border-bottom: 1px solid var(--border-color);
            padding: 1.5rem 2rem;
            position: sticky;
            top: 0;
            z-index: 100;
        }

        .header-content {
            max-width: 1200px;
            margin: 0 auto;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        .logo {
            font-size: 1.25rem;
            font-weight: 600;
            color: var(--text-primary);
        }

        .logo span {
            color: var(--accent-blue);
        }

        .status-badge {
            background: rgba(63, 185, 80, 0.1);
            border: 1px solid var(--accent-green);
            color: var(--accent-green);
            padding: 0.375rem 0.75rem;
            border-radius: 6px;
            font-size: 0.875rem;
            font-weight: 500;
        }

        .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 2rem;
        }

        .section {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            padding: 1.5rem;
            margin-bottom: 1.5rem;
        }

        .section-header {
            padding-bottom: 1rem;
            margin-bottom: 1.5rem;
            border-bottom: 1px solid var(--border-color);
        }

        .section-title {
            font-size: 1rem;
            font-weight: 600;
            color: var(--text-primary);
            margin-bottom: 0.25rem;
        }

        .section-subtitle {
            font-size: 0.8125rem;
            color: var(--text-secondary);
        }

        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
            gap: 1rem;
        }

        .stat-card {
            background: var(--bg-tertiary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            padding: 1.25rem;
            transition: border-color 0.2s;
        }

        .stat-card:hover {
            border-color: var(--accent-blue);
        }

        .stat-label {
            font-size: 0.75rem;
            color: var(--text-secondary);
            text-transform: uppercase;
            letter-spacing: 0.5px;
            margin-bottom: 0.5rem;
            font-weight: 500;
        }

        .stat-value {
            font-size: 2.5rem;
            font-weight: 700;
            color: var(--text-primary);
            font-variant-numeric: tabular-nums;
        }

        .stat-value.uptime {
            font-size: 1.75rem;
        }

        .endpoints-list {
            display: grid;
            gap: 0.75rem;
        }

        .endpoint {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 1rem;
            background: var(--bg-tertiary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            transition: all 0.2s;
        }

        .endpoint:hover {
            border-color: var(--accent-blue);
            background: rgba(88, 166, 255, 0.05);
        }

        .endpoint-info {
            display: flex;
            align-items: center;
            gap: 1rem;
        }

        .endpoint-method {
            font-family: 'Monaco', 'Menlo', 'Consolas', monospace;
            font-size: 0.75rem;
            font-weight: 700;
            color: var(--accent-green);
            background: rgba(63, 185, 80, 0.1);
            padding: 0.25rem 0.5rem;
            border-radius: 4px;
            min-width: 40px;
            text-align: center;
        }

        .endpoint-path {
            font-family: 'Monaco', 'Menlo', 'Consolas', monospace;
            font-size: 0.9375rem;
            color: var(--accent-blue);
            font-weight: 500;
        }

        .btn {
            background: var(--bg-tertiary);
            border: 1px solid var(--border-color);
            color: var(--text-primary);
            padding: 0.5rem 1rem;
            border-radius: 6px;
            cursor: pointer;
            text-decoration: none;
            display: inline-block;
            transition: all 0.2s;
            font-size: 0.875rem;
            font-weight: 500;
        }

        .btn:hover {
            background: var(--border-color);
        }

        .btn.primary {
            background: var(--accent-blue);
            border-color: var(--accent-blue);
            color: #000;
        }

        .btn.primary:hover {
            opacity: 0.9;
        }

        .dashboard-link {
            text-align: center;
            padding: 1.5rem;
            background: var(--bg-tertiary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            margin-top: 1rem;
        }

        .log-container {
            background: var(--bg-tertiary);
            border-radius: 6px;
            padding: 1rem;
            max-height: 350px;
            overflow-y: auto;
        }

        .log-entry {
            font-family: 'Monaco', 'Menlo', 'Consolas', monospace;
            font-size: 0.8125rem;
            padding: 0.5rem 0;
            border-bottom: 1px solid var(--border-color);
            display: flex;
            gap: 1rem;
        }

        .log-entry:last-child {
            border-bottom: none;
        }

        .log-time {
            color: var(--text-secondary);
            min-width: 80px;
            flex-shrink: 0;
        }

        .log-message {
            color: var(--text-primary);
        }

        .toast-container {
            position: fixed;
            top: 1rem;
            right: 1rem;
            z-index: 1000;
            display: flex;
            flex-direction: column;
            gap: 0.5rem;
        }

        .toast {
            background: var(--bg-secondary);
            border: 1px solid var(--border-color);
            border-radius: 6px;
            padding: 1rem 1.5rem;
            min-width: 300px;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
            display: flex;
            align-items: center;
            gap: 1rem;
            animation: slideIn 0.3s ease-out;
        }

        .toast.success {
            border-left: 3px solid var(--accent-green);
        }

        .toast.error {
            border-left: 3px solid var(--accent-red);
        }

        .toast-icon {
            font-size: 1.25rem;
            flex-shrink: 0;
        }

        .toast-content {
            flex: 1;
        }

        .toast-title {
            font-weight: 600;
            color: var(--text-primary);
            margin-bottom: 0.25rem;
        }

        .toast-message {
            font-size: 0.875rem;
            color: var(--text-secondary);
        }

        @keyframes slideIn {
            from {
                transform: translateX(100%);
                opacity: 0;
            }
            to {
                transform: translateX(0);
                opacity: 1;
            }
        }

        @media (max-width: 768px) {
            .header-content {
                flex-direction: column;
                gap: 1rem;
                align-items: flex-start;
            }

            .stats-grid {
                grid-template-columns: repeat(2, 1fr);
            }

            .endpoint-info {
                flex-direction: column;
                align-items: flex-start;
                gap: 0.5rem;
            }
        }
    </style>
</head>
<body>
    <div class="toast-container" id="toastContainer"></div>

    <div class="header">
        <div class="header-content">
            <div class="logo">
                <span>●</span> TinyObs Example App
            </div>
            <div class="status-badge">● Traffic Simulator Running</div>
        </div>
    </div>

    <div class="container">
        <div class="section">
            <div class="section-header">
                <div class="section-title">Live Application Metrics</div>
                <div class="section-subtitle">Real-time statistics from example application</div>
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
                    <div class="stat-value uptime" id="uptime">0s</div>
                </div>
            </div>
        </div>

        <div class="section">
            <div class="section-header">
                <div class="section-title">API Endpoints</div>
                <div class="section-subtitle">Available REST endpoints for testing</div>
            </div>
            <div class="endpoints-list">
                <div class="endpoint">
                    <div class="endpoint-info">
                        <span class="endpoint-method">GET</span>
                        <span class="endpoint-path">/api/users</span>
                    </div>
                    <button class="btn" onclick="testEndpoint('/api/users')">Test</button>
                </div>
                <div class="endpoint">
                    <div class="endpoint-info">
                        <span class="endpoint-method">GET</span>
                        <span class="endpoint-path">/api/orders</span>
                    </div>
                    <button class="btn" onclick="testEndpoint('/api/orders')">Test</button>
                </div>
                <div class="endpoint">
                    <div class="endpoint-info">
                        <span class="endpoint-method">GET</span>
                        <span class="endpoint-path">/api/products</span>
                    </div>
                    <button class="btn" onclick="testEndpoint('/api/products')">Test</button>
                </div>
                <div class="endpoint">
                    <div class="endpoint-info">
                        <span class="endpoint-method">GET</span>
                        <span class="endpoint-path">/health</span>
                    </div>
                    <button class="btn" onclick="testEndpoint('/health')">Test</button>
                </div>
            </div>
            <div class="dashboard-link">
                <a href="http://localhost:8080/dashboard.html" class="btn primary" style="padding: 1rem 2rem; font-size: 1rem;">
                    Open TinyObs Dashboard →
                </a>
            </div>
        </div>

        <div class="section">
            <div class="section-header">
                <div class="section-title">Activity Log</div>
                <div class="section-subtitle">Recent application events</div>
            </div>
            <div class="log-container" id="logs"></div>
        </div>
    </div>

    <script>
        const logs = [];
        const maxLogs = 20;

        function showToast(type, title, message) {
            const container = document.getElementById('toastContainer');
            const toast = document.createElement('div');
            toast.className = 'toast ' + type;

            const icon = type === 'success' ? '✅' : '❌';
            toast.innerHTML = '<div class="toast-icon">' + icon + '</div>' +
                '<div class="toast-content">' +
                    '<div class="toast-title">' + title + '</div>' +
                    '<div class="toast-message">' + message + '</div>' +
                '</div>';

            container.appendChild(toast);

            setTimeout(function() {
                toast.style.opacity = '0';
                toast.style.transform = 'translateX(100%)';
                setTimeout(function() { toast.remove(); }, 300);
            }, 3000);
        }

        async function testEndpoint(path) {
            const startTime = Date.now();
            try {
                const response = await fetch(path);
                const duration = Date.now() - startTime;

                if (response.ok) {
                    const data = await response.json();
                    showToast('success', path + ' - ' + response.status, 'Response time: ' + duration + 'ms');
                    addLog('✅ ' + path + ' - 200 OK (' + duration + 'ms)');
                } else {
                    showToast('error', path + ' - ' + response.status, 'Request failed');
                    addLog('❌ ' + path + ' - ' + response.status + ' (' + duration + 'ms)');
                }
            } catch (error) {
                const duration = Date.now() - startTime;
                showToast('error', path + ' - Error', error.message);
                addLog('❌ ' + path + ' - ' + error.message + ' (' + duration + 'ms)');
            }
        }

        function addLog(message) {
            const time = new Date().toLocaleTimeString();
            logs.unshift({ time, message });
            if (logs.length > maxLogs) logs.pop();

            const container = document.getElementById('logs');
            container.innerHTML = logs.map(log =>
                '<div class="log-entry"><span class="log-time">' + log.time + '</span><span class="log-message">' + log.message + '</span></div>'
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
	if err := tmpl.Execute(w, nil); err != nil {
		log.Printf("❌ Failed to execute template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
