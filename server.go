package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/encoder"
	"github.com/oszuidwest/zwfm-encoder/internal/server"
)

// Server is an HTTP server that provides the web interface for the audio encoder.
type Server struct {
	config   *config.Config
	encoder  *encoder.Encoder
	sessions *server.SessionManager
	commands *server.CommandHandler
	version  *VersionChecker
}

// NewServer returns a new Server configured with the provided config and encoder.
func NewServer(cfg *config.Config, enc *encoder.Encoder) *Server {
	sessions := server.NewSessionManager()
	commands := server.NewCommandHandler(
		cfg,
		enc.GetState,
		enc.StartOutput,
		enc.StopOutput,
		enc.Restart,
		enc.TriggerTestEmail,
	)

	return &Server{
		config:   cfg,
		encoder:  enc,
		sessions: sessions,
		commands: commands,
		version:  NewVersionChecker(),
	}
}

// handleWebSocket streams real-time encoder status and audio levels to the client.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := server.UpgradeConnection(w, r)
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
			var cmd server.WSCommand
			if err := conn.ReadJSON(&cmd); err != nil {
				close(done)
				return
			}
			s.commands.Handle(cmd, conn, func() {
				select {
				case statusUpdate <- true:
				default:
				}
			})
		}
	}()

	levelsTicker := time.NewTicker(100 * time.Millisecond) // 10 fps for VU meter
	statusTicker := time.NewTicker(3 * time.Second)
	defer levelsTicker.Stop()
	defer statusTicker.Stop()

	// Helper to send status
	sendStatus := func() error {
		status := s.encoder.GetStatus()
		status.OutputCount = len(s.config.GetOutputs())
		return conn.WriteJSON(map[string]interface{}{
			"type":              "status",
			"encoder":           status,
			"outputs":           s.config.GetOutputs(),
			"output_status":     s.encoder.GetAllOutputStatuses(),
			"devices":           encoder.ListAudioDevices(),
			"silence_threshold": s.config.GetSilenceThreshold(),
			"silence_duration":  s.config.GetSilenceDuration(),
			"silence_recovery":  s.config.GetSilenceRecovery(),
			"silence_webhook":   s.config.GetSilenceWebhook(),
			"email_smtp_host":   s.config.GetEmailSMTPHost(),
			"email_smtp_port":   s.config.GetEmailSMTPPort(),
			"email_username":    s.config.GetEmailUsername(),
			"email_recipients":  s.config.GetEmailRecipients(),
			"settings": map[string]interface{}{
				"audio_input": s.config.GetAudioInput(),
				"platform":    runtime.GOOS,
			},
			"version": s.version.GetInfo(),
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
				"levels": s.encoder.GetAudioLevels(),
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

// SetupRoutes returns an [http.Handler] configured with all application routes.
func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()
	basicAuth := s.sessions.AuthMiddleware(s.config.GetWebUser(), s.config.GetWebPassword())

	// WebSocket for all real-time communication (protected by basic auth)
	mux.HandleFunc("/ws", basicAuth(s.handleWebSocket))

	// Static files (also protected)
	mux.HandleFunc("/", basicAuth(s.handleStatic))

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
		html := strings.Replace(indexHTML, "{{VERSION}}", Version, 1)
		html = strings.Replace(html, "{{YEAR}}", fmt.Sprintf("%d", time.Now().Year()), 1)
		if _, err := w.Write([]byte(html)); err != nil {
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
	case "/alpine.min.js":
		w.Header().Set("Content-Type", "application/javascript")
		if _, err := w.Write([]byte(alpineJS)); err != nil {
			log.Printf("Failed to write alpine.min.js: %v", err)
		}
	default:
		http.NotFound(w, r)
	}
}

// Start begins listening and serving HTTP requests on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.GetWebPort())
	log.Printf("Starting web server on %s", addr)
	return http.ListenAndServe(addr, s.SetupRoutes())
}

// Conn is an alias for websocket.Conn to avoid import in other packages.
type Conn = websocket.Conn
