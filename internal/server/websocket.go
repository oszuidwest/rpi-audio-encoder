package server

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		// Allow requests without Origin header (same-origin requests)
		if origin == "" {
			return true
		}
		// Allow localhost and local network origins
		host := r.Host
		if strings.HasPrefix(origin, "http://"+host) || strings.HasPrefix(origin, "https://"+host) {
			return true
		}
		if strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") {
			return true
		}
		// Allow local network IPs (192.168.x.x, 10.x.x.x, 172.16-31.x.x)
		if strings.Contains(origin, "192.168.") || strings.Contains(origin, "10.") {
			return true
		}
		slog.Warn("rejected WebSocket connection", "origin", origin)
		return false
	},
}

// SafeConn wraps a WebSocket connection with a mutex for thread-safe writes.
// gorilla/websocket connections are not safe for concurrent writes.
type SafeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewSafeConn creates a new thread-safe WebSocket connection wrapper.
func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{conn: conn}
}

// WriteJSON writes a JSON message to the WebSocket connection (thread-safe).
func (c *SafeConn) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// ReadJSON reads a JSON message from the WebSocket connection.
// Reading is typically done from a single goroutine, so no mutex needed.
func (c *SafeConn) ReadJSON(v interface{}) error {
	return c.conn.ReadJSON(v)
}

// Close closes the underlying WebSocket connection.
func (c *SafeConn) Close() error {
	return c.conn.Close()
}

// UpgradeConnection upgrades an HTTP connection to WebSocket.
func UpgradeConnection(w http.ResponseWriter, r *http.Request) (*SafeConn, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return NewSafeConn(conn), nil
}
