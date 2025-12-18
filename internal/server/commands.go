package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/notify"
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
	cfg            *config.Config
	getState       func() types.EncoderState
	startOutput    func(string) error
	stopOutput     func(string) error
	startRecording func(string) error
	stopRecording  func(string) error
	restartEnc     func() error
	testTriggers   map[string]func() error
}

// NewCommandHandler creates a new command handler.
func NewCommandHandler(
	cfg *config.Config,
	getState func() types.EncoderState,
	startOutput func(string) error,
	stopOutput func(string) error,
	startRecording func(string) error,
	stopRecording func(string) error,
	restartEnc func() error,
	testTriggers map[string]func() error,
) *CommandHandler {
	return &CommandHandler{
		cfg:            cfg,
		getState:       getState,
		startOutput:    startOutput,
		stopOutput:     stopOutput,
		startRecording: startRecording,
		stopRecording:  stopRecording,
		restartEnc:     restartEnc,
		testTriggers:   testTriggers,
	}
}

// Handle processes a WebSocket command and performs the requested action.
func (h *CommandHandler) Handle(cmd WSCommand, conn *websocket.Conn, triggerStatusUpdate func()) {
	switch cmd.Type {
	case "add_output":
		h.handleAddOutput(cmd)
	case "delete_output":
		h.handleDeleteOutput(cmd)
	case "add_recording":
		h.handleAddRecording(cmd)
	case "delete_recording":
		h.handleDeleteRecording(cmd)
	case "start_recording":
		h.handleStartRecording(cmd)
	case "stop_recording":
		h.handleStopRecording(cmd)
	case "update_settings":
		h.handleUpdateSettings(cmd)
	case "test_webhook", "test_log", "test_email":
		h.handleTest(conn, cmd.Type)
	case "view_silence_log":
		h.handleViewSilenceLog(conn)
	default:
		slog.Warn("unknown WebSocket command type", "type", cmd.Type)
	}

	triggerStatusUpdate()
}

func (h *CommandHandler) handleAddOutput(cmd WSCommand) {
	var output types.Output
	if err := json.Unmarshal(cmd.Data, &output); err != nil {
		slog.Warn("add_output: invalid JSON data", "error", err)
		return
	}
	// Validate required fields
	if err := util.ValidateRequired("host", output.Host); err != nil {
		slog.Warn("add_output: validation failed", "error", err.Message)
		return
	}
	if err := util.ValidatePort("port", output.Port); err != nil {
		slog.Warn("add_output: validation failed", "error", err.Message)
		return
	}
	// Validate optional fields
	if err := util.ValidateMaxLength("host", output.Host, 253); err != nil {
		slog.Warn("add_output: validation failed", "error", err.Message)
		return
	}
	if err := util.ValidateMaxLength("streamid", output.StreamID, 256); err != nil {
		slog.Warn("add_output: validation failed", "error", err.Message)
		return
	}
	// Limit number of outputs to prevent resource exhaustion
	if len(h.cfg.GetOutputs()) >= 10 {
		slog.Warn("add_output: maximum of 10 outputs reached")
		return
	}
	// Validate max_retries if provided
	if err := util.ValidateRange("max_retries", output.MaxRetries, 0, 9999); err != nil {
		slog.Warn("add_output: validation failed", "error", err.Message)
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
		slog.Error("add_output: failed to add", "error", err)
		return
	}
	slog.Info("add_output: added output", "host", output.Host, "port", output.Port)
	// Start if encoder running
	if h.getState() == types.StateRunning {
		outputs := h.cfg.GetOutputs()
		if len(outputs) > 0 {
			if err := h.startOutput(outputs[len(outputs)-1].ID); err != nil {
				slog.Error("add_output: failed to start output", "error", err)
			}
		}
	}
}

func (h *CommandHandler) handleDeleteOutput(cmd WSCommand) {
	if cmd.ID == "" {
		slog.Warn("delete_output: no ID provided")
		return
	}
	slog.Info("delete_output: deleting", "output_id", cmd.ID)
	if err := h.stopOutput(cmd.ID); err != nil {
		slog.Error("delete_output: failed to stop", "error", err)
	}
	if err := h.cfg.RemoveOutput(cmd.ID); err != nil {
		slog.Error("delete_output: failed to remove from config", "error", err)
	} else {
		slog.Info("delete_output: removed from config", "output_id", cmd.ID)
	}
}

func (h *CommandHandler) handleAddRecording(cmd WSCommand) {
	var recording types.Recording
	if err := json.Unmarshal(cmd.Data, &recording); err != nil {
		slog.Warn("add_recording: invalid JSON data", "error", err)
		return
	}
	// Validate required fields
	if err := util.ValidateRequired("name", recording.Name); err != nil {
		slog.Warn("add_recording: validation failed", "error", err.Message)
		return
	}
	if err := util.ValidateRequired("path", recording.Path); err != nil {
		slog.Warn("add_recording: validation failed", "error", err.Message)
		return
	}
	// Validate optional fields
	if err := util.ValidateMaxLength("name", recording.Name, 100); err != nil {
		slog.Warn("add_recording: validation failed", "error", err.Message)
		return
	}
	if err := util.ValidateMaxLength("path", recording.Path, 500); err != nil {
		slog.Warn("add_recording: validation failed", "error", err.Message)
		return
	}
	// Limit number of recordings
	if len(h.cfg.GetRecordings()) >= 5 {
		slog.Warn("add_recording: maximum of 5 recordings reached")
		return
	}
	// Validate codec if provided
	if recording.Codec != "" {
		validCodecs := map[string]bool{"mp3": true, "mp2": true, "ogg": true, "wav": true}
		if !validCodecs[recording.Codec] {
			slog.Warn("add_recording: invalid codec, using default", "codec", recording.Codec)
			recording.Codec = "mp3"
		}
	}
	// Validate mode if provided
	if recording.Mode != "" && recording.Mode != types.RecordingModeAuto && recording.Mode != types.RecordingModeManual {
		slog.Warn("add_recording: invalid mode, using default", "mode", recording.Mode)
		recording.Mode = types.RecordingModeAuto
	}
	// Validate retention days if provided
	if recording.RetentionDays != 0 {
		if err := util.ValidateRange("retention_days", recording.RetentionDays, 1, 365); err != nil {
			slog.Warn("add_recording: validation failed", "error", err.Message)
			return
		}
	}
	if err := h.cfg.AddRecording(recording); err != nil {
		slog.Error("add_recording: failed to add", "error", err)
		return
	}
	slog.Info("add_recording: added recording", "name", recording.Name, "path", recording.Path, "mode", recording.Mode)
	// Start if encoder running and mode is auto (or empty which defaults to auto)
	if h.getState() == types.StateRunning {
		recordings := h.cfg.GetRecordings()
		if len(recordings) > 0 {
			newRecording := recordings[len(recordings)-1]
			// Only auto-start recordings in auto mode (or unset mode which defaults to auto)
			if newRecording.IsAuto() {
				if err := h.startRecording(newRecording.ID); err != nil {
					slog.Error("add_recording: failed to start recording", "error", err)
				}
			}
		}
	}
}

func (h *CommandHandler) handleDeleteRecording(cmd WSCommand) {
	if cmd.ID == "" {
		slog.Warn("delete_recording: no ID provided")
		return
	}
	slog.Info("delete_recording: deleting", "recording_id", cmd.ID)
	if err := h.stopRecording(cmd.ID); err != nil {
		slog.Error("delete_recording: failed to stop", "error", err)
	}
	if err := h.cfg.RemoveRecording(cmd.ID); err != nil {
		slog.Error("delete_recording: failed to remove from config", "error", err)
	} else {
		slog.Info("delete_recording: removed from config", "recording_id", cmd.ID)
	}
}

func (h *CommandHandler) handleStartRecording(cmd WSCommand) {
	if cmd.ID == "" {
		slog.Warn("start_recording: no ID provided")
		return
	}
	if h.getState() != types.StateRunning {
		slog.Warn("start_recording: encoder not running")
		return
	}
	slog.Info("start_recording: starting", "recording_id", cmd.ID)
	if err := h.startRecording(cmd.ID); err != nil {
		slog.Error("start_recording: failed to start", "error", err)
	}
}

func (h *CommandHandler) handleStopRecording(cmd WSCommand) {
	if cmd.ID == "" {
		slog.Warn("stop_recording: no ID provided")
		return
	}
	slog.Info("stop_recording: stopping", "recording_id", cmd.ID)
	if err := h.stopRecording(cmd.ID); err != nil {
		slog.Error("stop_recording: failed to stop", "error", err)
	}
}

// updateFloatSetting validates and updates a float64 setting with logging.
func updateFloatSetting(value *float64, min, max float64, name string, setter func(float64) error) {
	if value == nil {
		return
	}
	v := *value
	if err := util.ValidateRangeFloat(name, v, min, max); err != nil {
		slog.Warn("update_settings: validation failed", "setting", name, "error", err.Message)
		return
	}
	slog.Info("update_settings: changing setting", "setting", name, "value", v)
	if err := setter(v); err != nil {
		slog.Error("update_settings: failed to save", "error", err)
	}
}

// updateStringSetting updates a string setting with logging.
func updateStringSetting(value *string, name string, setter func(string) error) {
	if value == nil {
		return
	}
	slog.Info("update_settings: changing setting", "setting", name)
	if err := setter(*value); err != nil {
		slog.Error("update_settings: failed to save", "error", err)
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
		slog.Warn("update_settings: invalid JSON data", "error", err)
		return
	}
	if settings.AudioInput != "" {
		slog.Info("update_settings: changing audio input", "input", settings.AudioInput)
		if err := h.cfg.SetAudioInput(settings.AudioInput); err != nil {
			slog.Error("update_settings: failed to save", "error", err)
		}
		if h.getState() == types.StateRunning {
			go func() {
				if err := h.restartEnc(); err != nil {
					slog.Error("update_settings: failed to restart encoder", "error", err)
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
				slog.Warn("update_settings: invalid SMTP port, using default", "port", port)
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

		slog.Info("update_settings: updating email configuration")
		if err := h.cfg.SetEmailConfig(host, port, username, password, recipients); err != nil {
			slog.Error("update_settings: failed to save email config", "error", err)
		}
	}
}

// handleTest executes a notification test and sends the result to the client.
// testCmd should be in format "test_<type>" (e.g., "test_email", "test_webhook").
func (h *CommandHandler) handleTest(conn *websocket.Conn, testCmd string) {
	testType := strings.TrimPrefix(testCmd, "test_")
	trigger, ok := h.testTriggers[testType]
	if !ok {
		slog.Warn("unknown test type", "command", testCmd)
		return
	}

	go func() {
		result := map[string]interface{}{
			"type":      "test_result",
			"test_type": testType,
			"success":   true,
		}

		if err := trigger(); err != nil {
			slog.Error("test failed", "command", testCmd, "error", err)
			result["success"] = false
			result["error"] = err.Error()
		} else {
			slog.Info("test succeeded", "command", testCmd)
		}

		if wsErr := conn.WriteJSON(result); wsErr != nil {
			slog.Error("failed to send test response", "command", testCmd, "error", wsErr)
		}
	}()
}

// handleViewSilenceLog reads and returns the silence log file contents.
func (h *CommandHandler) handleViewSilenceLog(conn *websocket.Conn) {
	go func() {
		result := map[string]interface{}{
			"type":    "silence_log_result",
			"success": true,
		}

		logPath := h.cfg.GetSilenceLogPath()
		if logPath == "" {
			result["success"] = false
			result["error"] = "Log file path not configured"
			if wsErr := conn.WriteJSON(result); wsErr != nil {
				slog.Error("failed to send silence log response", "error", wsErr)
			}
			return
		}

		entries, err := readSilenceLog(logPath, 100)
		if err != nil {
			result["success"] = false
			result["error"] = err.Error()
		} else {
			result["entries"] = entries
			result["path"] = logPath
		}

		if wsErr := conn.WriteJSON(result); wsErr != nil {
			slog.Error("failed to send silence log response", "error", wsErr)
		}
	}()
}

// readSilenceLog reads the last N entries from the silence log file.
func readSilenceLog(logPath string, maxEntries int) ([]notify.SilenceLogEntry, error) {
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return []notify.SilenceLogEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return []notify.SilenceLogEntry{}, nil
	}

	// Take last N lines (most recent entries)
	start := 0
	if len(lines) > maxEntries {
		start = len(lines) - maxEntries
	}
	lines = lines[start:]

	entries := make([]notify.SilenceLogEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry notify.SilenceLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // Skip malformed entries
		}
		entries = append(entries, entry)
	}

	// Reverse to show newest first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries, nil
}
