package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// upgrader configures the WebSocket connection upgrader with origin validation
// that allows same-origin, localhost, and local network connections.
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

// Server is an HTTP server that provides the web interface for the audio encoder.
type Server struct {
	config  *Config
	manager *Encoder
}

// NewServer returns a new Server configured with the provided config and FFmpeg manager.
func NewServer(config *Config, manager *Encoder) *Server {
	return &Server{
		config:  config,
		manager: manager,
	}
}

// basicAuth returns an HTTP handler that wraps the provided handler with
// HTTP Basic Authentication using credentials from the server's config.
func (s *Server) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.config.WebUser || pass != s.config.WebPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Encoder"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// WSCommand is a command received from a WebSocket client.
type WSCommand struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// handleWebSocket upgrades the HTTP connection to WebSocket and manages
// bidirectional communication for real-time encoder status and audio levels.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("Failed to close WebSocket connection: %v", err)
		}
	}()

	// Channel to signal status update needed
	statusUpdate := make(chan bool, 1)
	done := make(chan bool)

	// Goroutine to read and process commands from client
	go func() {
		for {
			var cmd WSCommand
			if err := conn.ReadJSON(&cmd); err != nil {
				close(done)
				return
			}
			s.handleWSCommand(cmd, statusUpdate)
		}
	}()

	levelsTicker := time.NewTicker(100 * time.Millisecond) // 10 fps for VU meter
	statusTicker := time.NewTicker(3 * time.Second)
	defer levelsTicker.Stop()
	defer statusTicker.Stop()

	// Helper to send status
	sendStatus := func() error {
		status := s.manager.GetStatus()
		status.OutputCount = len(s.config.GetEnabledOutputs())
		return conn.WriteJSON(map[string]interface{}{
			"type":          "status",
			"encoder":       status,
			"outputs":       s.config.GetOutputs(),
			"output_status": s.manager.GetAllOutputStatuses(),
			"devices":       ListAudioDevices(),
			"settings": map[string]interface{}{
				"audio_input": s.config.GetAudioInput(),
				"platform":    runtime.GOOS,
			},
		})
	}

	// Send initial status
	if err := sendStatus(); err != nil {
		return
	}

	for {
		select {
		case <-done:
			return
		case <-statusUpdate:
			if err := sendStatus(); err != nil {
				return
			}
		case <-levelsTicker.C:
			if err := conn.WriteJSON(map[string]interface{}{
				"type":   "levels",
				"levels": s.manager.GetAudioLevels(),
			}); err != nil {
				return
			}
		case <-statusTicker.C:
			if err := sendStatus(); err != nil {
				return
			}
		}
	}
}

// handleWSCommand processes a WebSocket command from a client and performs
// the requested action (add_output, delete_output, or update_settings).
func (s *Server) handleWSCommand(cmd WSCommand, statusUpdate chan<- bool) {
	switch cmd.Type {
	case "add_output":
		var output Output
		if err := json.Unmarshal(cmd.Data, &output); err != nil {
			log.Printf("add_output: invalid JSON data: %v", err)
			return
		}
		// Validate required fields
		if output.Host == "" {
			log.Printf("add_output: host is required")
			return
		}
		if output.Port < 1 || output.Port > 65535 {
			log.Printf("add_output: port must be between 1 and 65535, got %d", output.Port)
			return
		}
		// Validate optional fields
		if len(output.Host) > 253 {
			log.Printf("add_output: host too long (max 253 chars)")
			return
		}
		if len(output.StreamID) > 256 {
			log.Printf("add_output: streamid too long (max 256 chars)")
			return
		}
		// Limit number of outputs to prevent resource exhaustion
		if len(s.config.GetOutputs()) >= 10 {
			log.Printf("add_output: maximum of 10 outputs reached")
			return
		}
		// Set defaults
		if output.StreamID == "" {
			output.StreamID = "studio"
		}
		if output.Codec == "" {
			output.Codec = "mp3"
		}
		output.Enabled = true
		if err := s.config.AddOutput(output); err != nil {
			log.Printf("add_output: failed to add: %v", err)
			return
		}
		log.Printf("add_output: added %s:%d", output.Host, output.Port)
		// Start if encoder running
		if s.manager.GetState() == StateRunning {
			outputs := s.config.GetOutputs()
			if len(outputs) > 0 {
				if err := s.manager.StartOutput(outputs[len(outputs)-1].ID); err != nil {
					log.Printf("add_output: failed to start output: %v", err)
				}
			}
		}

	case "delete_output":
		if cmd.ID == "" {
			log.Printf("delete_output: no ID provided")
			return
		}
		log.Printf("delete_output: deleting %s", cmd.ID)
		if err := s.manager.StopOutput(cmd.ID); err != nil {
			log.Printf("delete_output: failed to stop: %v", err)
		}
		if err := s.config.RemoveOutput(cmd.ID); err != nil {
			log.Printf("delete_output: failed to remove from config: %v", err)
		} else {
			log.Printf("delete_output: removed %s from config", cmd.ID)
		}

	case "update_settings":
		var settings struct {
			AudioInput string `json:"audio_input"`
		}
		if err := json.Unmarshal(cmd.Data, &settings); err != nil {
			log.Printf("update_settings: invalid JSON data: %v", err)
			return
		}
		if settings.AudioInput != "" {
			log.Printf("update_settings: changing audio input to %s", settings.AudioInput)
			if err := s.config.SetAudioInput(settings.AudioInput); err != nil {
				log.Printf("update_settings: failed to save: %v", err)
			}
			if s.manager.GetState() == StateRunning {
				go func() {
					if err := s.manager.Restart(); err != nil {
						log.Printf("update_settings: failed to restart encoder: %v", err)
					}
				}()
			}
		}

	default:
		log.Printf("Unknown WebSocket command type: %s", cmd.Type)
	}

	// Trigger status update
	select {
	case statusUpdate <- true:
	default:
	}
}

// SetupRoutes returns an [http.Handler] configured with all application routes.
func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	// WebSocket for all real-time communication (protected by basic auth)
	mux.HandleFunc("/ws", s.basicAuth(s.handleWebSocket))

	// Static files (also protected)
	mux.HandleFunc("/", s.basicAuth(s.handleStatic))

	return mux
}

// handleStatic serves the embedded static web interface files.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// Serve embedded static content
	switch path {
	case "/index.html":
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(indexHTML)); err != nil {
			log.Printf("Failed to write index.html: %v", err)
		}
	case "/style.css":
		w.Header().Set("Content-Type", "text/css")
		if _, err := w.Write([]byte(styleCSS)); err != nil {
			log.Printf("Failed to write style.css: %v", err)
		}
	case "/app.js":
		w.Header().Set("Content-Type", "application/javascript")
		if _, err := w.Write([]byte(appJS)); err != nil {
			log.Printf("Failed to write app.js: %v", err)
		}
	default:
		http.NotFound(w, r)
	}
}

// Start begins listening and serving HTTP requests on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.WebPort)
	log.Printf("Starting web server on %s", addr)
	return http.ListenAndServe(addr, s.SetupRoutes())
}
