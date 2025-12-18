package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/encoder"
	"github.com/oszuidwest/zwfm-encoder/internal/server"
	"github.com/oszuidwest/zwfm-encoder/internal/util"
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
		enc.StartRecording,
		enc.StopRecording,
		enc.Restart,
		map[string]func() error{
			"webhook": enc.TriggerTestWebhook,
			"log":     enc.TriggerTestLog,
			"email":   enc.TriggerTestEmail,
		},
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
		slog.Error("WebSocket upgrade failed", "error", err)
		return
	}
	defer util.SafeCloseFunc(conn, "WebSocket connection")()

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
			"recordings":        s.config.GetRecordings(),
			"recording_status":  s.encoder.GetAllRecordingStatuses(),
			"devices":           encoder.ListAudioDevices(),
			"silence_threshold": s.config.GetSilenceThreshold(),
			"silence_duration":  s.config.GetSilenceDuration(),
			"silence_recovery":  s.config.GetSilenceRecovery(),
			"silence_webhook":   s.config.GetSilenceWebhook(),
			"silence_log_path":  s.config.GetSilenceLogPath(),
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

// staticFile represents an embedded static file with its content type and content.
type staticFile struct {
	contentType string
	content     string
	name        string
}

// staticFiles maps URL paths to their corresponding static file definitions.
var staticFiles = map[string]staticFile{
	"/style.css": {
		contentType: "text/css",
		content:     styleCSS,
		name:        "style.css",
	},
	"/app.js": {
		contentType: "application/javascript",
		content:     appJS,
		name:        "app.js",
	},
	"/icons.js": {
		contentType: "application/javascript",
		content:     iconsJS,
		name:        "icons.js",
	},
	"/alpine.min.js": {
		contentType: "application/javascript",
		content:     alpineJS,
		name:        "alpine.min.js",
	},
}

// handleStatic serves the embedded static web interface files.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// Handle index.html specially (requires template replacement)
	if path == "/index.html" {
		w.Header().Set("Content-Type", "text/html")
		html := strings.Replace(indexHTML, "{{VERSION}}", Version, 1)
		html = strings.ReplaceAll(html, "{{YEAR}}", fmt.Sprintf("%d", time.Now().Year()))
		if _, err := w.Write([]byte(html)); err != nil {
			slog.Error("failed to write index.html", "error", err)
		}
		return
	}

	// Handle other static files via table lookup
	if file, ok := staticFiles[path]; ok {
		w.Header().Set("Content-Type", file.contentType)
		if _, err := w.Write([]byte(file.content)); err != nil {
			slog.Error("failed to write static file", "file", file.name, "error", err)
		}
		return
	}

	// File not found
	http.NotFound(w, r)
}

// Start begins listening and serving HTTP requests on the configured port.
// Returns an *http.Server that can be used for graceful shutdown.
func (s *Server) Start() *http.Server {
	addr := fmt.Sprintf(":%d", s.config.GetWebPort())
	slog.Info("starting web server", "addr", addr)

	srv := &http.Server{
		Addr:    addr,
		Handler: s.SetupRoutes(),
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
		}
	}()

	return srv
}

// Conn is an alias for websocket.Conn to avoid import in other packages.
type Conn = websocket.Conn
