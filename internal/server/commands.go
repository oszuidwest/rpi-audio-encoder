package server

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
	"github.com/oszuidwest/zwfm-encoder/internal/util"
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
	testTriggers map[string]func() error
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(
	cfg *config.Config,
	getState func() types.EncoderState,
	startOutput func(string) error,
	stopOutput func(string) error,
	restartEnc func() error,
	testTriggers map[string]func() error,
) *CommandHandler {
	return &CommandHandler{
		cfg:          cfg,
		getState:     getState,
		startOutput:  startOutput,
		stopOutput:   stopOutput,
		restartEnc:   restartEnc,
		testTriggers: testTriggers,
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
	case "test_webhook", "test_log", "test_email":
		h.handleTest(conn, cmd.Type)
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
	if err := util.ValidateRequired("host", output.Host); err != nil {
		log.Printf("add_output: %s", err.Message)
		return
	}
	if err := util.ValidatePort("port", output.Port); err != nil {
		log.Printf("add_output: %s", err.Message)
		return
	}
	// Validate optional fields
	if err := util.ValidateMaxLength("host", output.Host, 253); err != nil {
		log.Printf("add_output: %s", err.Message)
		return
	}
	if err := util.ValidateMaxLength("streamid", output.StreamID, 256); err != nil {
		log.Printf("add_output: %s", err.Message)
		return
	}
	// Limit number of outputs to prevent resource exhaustion
	if len(h.cfg.GetOutputs()) >= 10 {
		log.Printf("add_output: maximum of 10 outputs reached")
		return
	}
	// Validate max_retries if provided
	if err := util.ValidateRange("max_retries", output.MaxRetries, 0, 9999); err != nil {
		log.Printf("add_output: %s", err.Message)
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

// updateFloatSetting validates and updates a float64 setting with logging.
func updateFloatSetting(value *float64, min, max float64, name string, setter func(float64) error) {
	if value == nil {
		return
	}
	v := *value
	if err := util.ValidateRangeFloat(name, v, min, max); err != nil {
		log.Printf("update_settings: %s", err.Message)
		return
	}
	log.Printf("update_settings: changing %s to %.1f", name, v)
	if err := setter(v); err != nil {
		log.Printf("update_settings: failed to save: %v", err)
	}
}

// updateStringSetting updates a string setting with logging.
func updateStringSetting(value *string, name string, setter func(string) error) {
	if value == nil {
		return
	}
	log.Printf("update_settings: changing %s", name)
	if err := setter(*value); err != nil {
		log.Printf("update_settings: failed to save: %v", err)
	}
}

func (h *CommandHandler) handleUpdateSettings(cmd WSCommand) {
	var settings struct {
		AudioInput       string   `json:"audio_input"`
		SilenceThreshold *float64 `json:"silence_threshold"`
		SilenceDuration  *float64 `json:"silence_duration"`
		SilenceRecovery  *float64 `json:"silence_recovery"`
		SilenceWebhook   *string  `json:"silence_webhook"`
		SilenceLogPath   *string  `json:"silence_log_path"`
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
	updateFloatSetting(settings.SilenceThreshold, -60, 0, "silence threshold", h.cfg.SetSilenceThreshold)
	updateFloatSetting(settings.SilenceDuration, 1, 300, "silence duration", h.cfg.SetSilenceDuration)
	updateFloatSetting(settings.SilenceRecovery, 1, 60, "silence recovery", h.cfg.SetSilenceRecovery)
	updateStringSetting(settings.SilenceWebhook, "silence webhook", h.cfg.SetSilenceWebhook)
	updateStringSetting(settings.SilenceLogPath, "silence log path", h.cfg.SetSilenceLogPath)
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

// handleTest executes a notification test and sends the result to the client.
// testCmd should be in format "test_<type>" (e.g., "test_email", "test_webhook").
func (h *CommandHandler) handleTest(conn *websocket.Conn, testCmd string) {
	testType := strings.TrimPrefix(testCmd, "test_")
	trigger, ok := h.testTriggers[testType]
	if !ok {
		log.Printf("%s: unknown test type", testCmd)
		return
	}

	go func() {
		result := map[string]interface{}{
			"type":      "test_result",
			"test_type": testType,
			"success":   true,
		}

		if err := trigger(); err != nil {
			log.Printf("%s: failed: %v", testCmd, err)
			result["success"] = false
			result["error"] = err.Error()
		} else {
			log.Printf("%s: success", testCmd)
		}

		if wsErr := conn.WriteJSON(result); wsErr != nil {
			log.Printf("%s: failed to send response: %v", testCmd, wsErr)
		}
	}()
}
