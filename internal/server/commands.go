package server

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// WSCommand is a command received from a WebSocket client.
type WSCommand struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// CommandHandler processes WebSocket commands.
type CommandHandler struct {
	cfg          *config.Config
	getState     func() types.EncoderState
	startOutput  func(string) error
	stopOutput   func(string) error
	restartEnc   func() error
	triggerEmail func() error
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(
	cfg *config.Config,
	getState func() types.EncoderState,
	startOutput func(string) error,
	stopOutput func(string) error,
	restartEnc func() error,
	triggerEmail func() error,
) *CommandHandler {
	return &CommandHandler{
		cfg:          cfg,
		getState:     getState,
		startOutput:  startOutput,
		stopOutput:   stopOutput,
		restartEnc:   restartEnc,
		triggerEmail: triggerEmail,
	}
}

// Handle processes a WebSocket command and performs the requested action.
func (h *CommandHandler) Handle(cmd WSCommand, conn *websocket.Conn, triggerStatusUpdate func()) {
	switch cmd.Type {
	case "add_output":
		h.handleAddOutput(cmd)
	case "delete_output":
		h.handleDeleteOutput(cmd)
	case "update_settings":
		h.handleUpdateSettings(cmd)
	case "test_email":
		h.handleTestEmail(conn)
	default:
		log.Printf("Unknown WebSocket command type: %s", cmd.Type)
	}

	triggerStatusUpdate()
}

func (h *CommandHandler) handleAddOutput(cmd WSCommand) {
	var output types.Output
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
	if len(h.cfg.GetOutputs()) >= 10 {
		log.Printf("add_output: maximum of 10 outputs reached")
		return
	}
	// Validate max_retries if provided
	if output.MaxRetries < 0 || output.MaxRetries > 9999 {
		log.Printf("add_output: max_retries must be between 0 and 9999, got %d", output.MaxRetries)
		return
	}
	// Set defaults
	if output.StreamID == "" {
		output.StreamID = "studio"
	}
	if output.Codec == "" {
		output.Codec = "mp3"
	}
	if err := h.cfg.AddOutput(output); err != nil {
		log.Printf("add_output: failed to add: %v", err)
		return
	}
	log.Printf("add_output: added %s:%d", output.Host, output.Port)
	// Start if encoder running
	if h.getState() == types.StateRunning {
		outputs := h.cfg.GetOutputs()
		if len(outputs) > 0 {
			if err := h.startOutput(outputs[len(outputs)-1].ID); err != nil {
				log.Printf("add_output: failed to start output: %v", err)
			}
		}
	}
}

func (h *CommandHandler) handleDeleteOutput(cmd WSCommand) {
	if cmd.ID == "" {
		log.Printf("delete_output: no ID provided")
		return
	}
	log.Printf("delete_output: deleting %s", cmd.ID)
	if err := h.stopOutput(cmd.ID); err != nil {
		log.Printf("delete_output: failed to stop: %v", err)
	}
	if err := h.cfg.RemoveOutput(cmd.ID); err != nil {
		log.Printf("delete_output: failed to remove from config: %v", err)
	} else {
		log.Printf("delete_output: removed %s from config", cmd.ID)
	}
}

func (h *CommandHandler) handleUpdateSettings(cmd WSCommand) {
	var settings struct {
		AudioInput       string   `json:"audio_input"`
		SilenceThreshold *float64 `json:"silence_threshold"`
		SilenceDuration  *float64 `json:"silence_duration"`
		SilenceRecovery  *float64 `json:"silence_recovery"`
		SilenceWebhook   *string  `json:"silence_webhook"`
		EmailSMTPHost    *string  `json:"email_smtp_host"`
		EmailSMTPPort    *int     `json:"email_smtp_port"`
		EmailUsername    *string  `json:"email_username"`
		EmailPassword    *string  `json:"email_password"`
		EmailRecipients  *string  `json:"email_recipients"`
	}
	if err := json.Unmarshal(cmd.Data, &settings); err != nil {
		log.Printf("update_settings: invalid JSON data: %v", err)
		return
	}
	if settings.AudioInput != "" {
		log.Printf("update_settings: changing audio input to %s", settings.AudioInput)
		if err := h.cfg.SetAudioInput(settings.AudioInput); err != nil {
			log.Printf("update_settings: failed to save: %v", err)
		}
		if h.getState() == types.StateRunning {
			go func() {
				if err := h.restartEnc(); err != nil {
					log.Printf("update_settings: failed to restart encoder: %v", err)
				}
			}()
		}
	}
	if settings.SilenceThreshold != nil {
		threshold := *settings.SilenceThreshold
		if threshold < -60 || threshold > 0 {
			log.Printf("update_settings: invalid silence threshold %.1f (must be -60 to 0)", threshold)
		} else {
			log.Printf("update_settings: changing silence threshold to %.1f dB", threshold)
			if err := h.cfg.SetSilenceThreshold(threshold); err != nil {
				log.Printf("update_settings: failed to save: %v", err)
			}
		}
	}
	if settings.SilenceDuration != nil {
		duration := *settings.SilenceDuration
		if duration < 1 || duration > 300 {
			log.Printf("update_settings: invalid silence duration %.1f (must be 1-300s)", duration)
		} else {
			log.Printf("update_settings: changing silence duration to %.1fs", duration)
			if err := h.cfg.SetSilenceDuration(duration); err != nil {
				log.Printf("update_settings: failed to save: %v", err)
			}
		}
	}
	if settings.SilenceRecovery != nil {
		recovery := *settings.SilenceRecovery
		if recovery < 1 || recovery > 60 {
			log.Printf("update_settings: invalid silence recovery %.1f (must be 1-60s)", recovery)
		} else {
			log.Printf("update_settings: changing silence recovery to %.1fs", recovery)
			if err := h.cfg.SetSilenceRecovery(recovery); err != nil {
				log.Printf("update_settings: failed to save: %v", err)
			}
		}
	}
	if settings.SilenceWebhook != nil {
		log.Printf("update_settings: changing silence webhook")
		if err := h.cfg.SetSilenceWebhook(*settings.SilenceWebhook); err != nil {
			log.Printf("update_settings: failed to save: %v", err)
		}
	}
	// Handle email configuration updates
	if settings.EmailSMTPHost != nil || settings.EmailSMTPPort != nil ||
		settings.EmailUsername != nil || settings.EmailPassword != nil ||
		settings.EmailRecipients != nil {

		// Get current values for fields not being updated
		host := h.cfg.GetEmailSMTPHost()
		port := h.cfg.GetEmailSMTPPort()
		username := h.cfg.GetEmailUsername()
		password := h.cfg.GetEmailPassword()
		recipients := h.cfg.GetEmailRecipients()

		// Apply updates
		if settings.EmailSMTPHost != nil {
			host = *settings.EmailSMTPHost
		}
		if settings.EmailSMTPPort != nil {
			port = *settings.EmailSMTPPort
			if port < 1 || port > 65535 {
				log.Printf("update_settings: invalid SMTP port %d, using default", port)
				port = config.DefaultEmailSMTPPort
			}
		}
		if settings.EmailUsername != nil {
			username = *settings.EmailUsername
		}
		if settings.EmailPassword != nil {
			password = *settings.EmailPassword
		}
		if settings.EmailRecipients != nil {
			recipients = *settings.EmailRecipients
		}

		log.Printf("update_settings: updating email configuration")
		if err := h.cfg.SetEmailConfig(host, port, username, password, recipients); err != nil {
			log.Printf("update_settings: failed to save email config: %v", err)
		}
	}
}

func (h *CommandHandler) handleTestEmail(conn *websocket.Conn) {
	go func() {
		if err := h.triggerEmail(); err != nil {
			log.Printf("test_email: failed: %v", err)
			// Send error response to client
			if wsErr := conn.WriteJSON(map[string]interface{}{
				"type":    "test_email_result",
				"success": false,
				"error":   err.Error(),
			}); wsErr != nil {
				log.Printf("test_email: failed to send response: %v", wsErr)
			}
		} else {
			log.Printf("test_email: sent successfully")
			// Send success response to client
			if wsErr := conn.WriteJSON(map[string]interface{}{
				"type":    "test_email_result",
				"success": true,
			}); wsErr != nil {
				log.Printf("test_email: failed to send response: %v", wsErr)
			}
		}
	}()
}
