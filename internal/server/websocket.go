package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// upgrader configures the WebSocket upgrader with origin validation for same-origin and local network connections.
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
		log.Printf("Rejected WebSocket connection from origin: %s", origin)
		return false
	},
}

// UpgradeConnection upgrades an HTTP connection to WebSocket.
func UpgradeConnection(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return upgrader.Upgrade(w, r, nil)
}
