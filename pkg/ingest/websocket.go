package ingest

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// WebSocket configuration
	wsReadBufferSize  = 1024
	wsWriteBufferSize = 1024
	wsBroadcastBuffer = 256
	wsChannelBuffer   = 10 // Buffer for register/unregister to prevent blocking
	wsWriteDeadline   = 10 * time.Second
	wsReadDeadline    = 60 * time.Second
	wsPingInterval    = 30 * time.Second
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		// Allow same-origin requests, or requests with no Origin header
		// No Origin header = direct connection (non-browser clients like curl, testing tools)
		return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
	},
	ReadBufferSize:  wsReadBufferSize,
	WriteBufferSize: wsWriteBufferSize,
}

// MetricsHub manages WebSocket connections for real-time metrics streaming
type MetricsHub struct {
	// Registered clients
	clients map[*websocket.Conn]bool

	// Register requests from clients
	register chan *websocket.Conn

	// Unregister requests from clients
	unregister chan *websocket.Conn

	// Broadcast channel for metrics updates
	broadcast chan []byte

	mu sync.RWMutex
}

// NewMetricsHub creates a new WebSocket hub
func NewMetricsHub() *MetricsHub {
	return &MetricsHub{
		clients:    make(map[*websocket.Conn]bool),
		register:   make(chan *websocket.Conn, wsChannelBuffer),
		unregister: make(chan *websocket.Conn, wsChannelBuffer),
		broadcast:  make(chan []byte, wsBroadcastBuffer),
	}
}

// Run starts the hub's main loop
func (h *MetricsHub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Close all client connections on shutdown
			h.mu.Lock()
			for conn := range h.clients {
				conn.Close()
			}
			h.mu.Unlock()
			return
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("📡 WebSocket client connected (total: %d)", count)
		case conn := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[conn]; ok {
				delete(h.clients, conn)
				conn.Close()
			}
			count := len(h.clients)
			h.mu.Unlock()
			log.Printf("📡 WebSocket client disconnected (total: %d)", count)
		case message := <-h.broadcast:
			h.mu.RLock()
			// Collect failed connections to unregister after releasing lock
			var failed []*websocket.Conn
			for conn := range h.clients {
				conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
				if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
					log.Printf("❌ WebSocket write error: %v", err)
					failed = append(failed, conn)
				}
			}
			h.mu.RUnlock()

			// Unregister failed connections without holding the lock
			for _, conn := range failed {
				h.unregister <- conn
			}
		}
	}
}

// Broadcast sends a message to all connected clients
func (h *MetricsHub) Broadcast(data interface{}) error {
	message, err := json.Marshal(data)
	if err != nil {
		return err
	}

	select {
	case h.broadcast <- message:
		return nil
	default:
		// Channel full, drop message to prevent blocking
		log.Printf("⚠️  Broadcast channel full, dropping message")
		return nil
	}
}

// HasClients returns true if there are any connected WebSocket clients
func (h *MetricsHub) HasClients() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients) > 0
}

// HandleWebSocket handles WebSocket upgrade requests
func (h *Handler) HandleWebSocket(hub *MetricsHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Upgrade HTTP connection to WebSocket
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("❌ WebSocket upgrade failed: %v", err)
			return
		}

		// Register the new client
		hub.register <- conn

		// Create context for managing goroutine lifecycle
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Start ping sender to keep connection alive
		go func() {
			ticker := time.NewTicker(wsPingInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		// Read loop handles ping/pong and detects connection close
		defer func() {
			cancel() // Signal ping goroutine to stop
			hub.unregister <- conn
		}()

		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
			return nil
		})

		// Read messages (mostly for handling control frames)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error: %v", err)
				}
				break
			}
		}
	}
}
