package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/nicktill/tinyobs/pkg/sdk/metrics"
)

// startTrafficSimulator starts a goroutine that generates predictable traffic
func startTrafficSimulator(ctx context.Context, activeUsers metrics.GaugeInterface) {
	// Give server a moment to fully start
	time.Sleep(500 * time.Millisecond)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	endpoints := []string{"/api/users", "/api/orders", "/api/products"}
	log.Println("🚦 Traffic simulator started - making requests every 3 seconds")
	log.Println("💡 You should see traffic appearing in TinyObs dashboard shortly...")

	reqCount := 0
	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Traffic simulator stopped")
			return
		case <-ticker.C:
			reqCount++

			// Cycle through endpoints in order for predictability
			endpoint := endpoints[reqCount%len(endpoints)]

			// Simulate steady increasing active users (1-20 range)
			activeUserCount := (reqCount % 20) + 1
			activeUsers.Set(float64(activeUserCount))

			// Make request to generate metrics
			go func(ep string, count int) {
				resp, err := http.Get("http://localhost:3000" + ep)
				if err != nil {
					log.Printf("⚠️  Traffic simulation failed for %s: %v", ep, err)
					return
				}
				defer resp.Body.Close()
				log.Printf("✅ Request #%d: %s → %d %s (active_users: %d)", count, ep, resp.StatusCode, resp.Status, activeUserCount)
			}(endpoint, reqCount)
		}
	}
}
