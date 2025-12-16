package notify

import (
	"encoding/json"
	"os"

	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// SilenceLogEntry represents a single log entry for silence events.
type SilenceLogEntry struct {
	Timestamp   string  `json:"timestamp"`
	Event       string  `json:"event"`
	DurationSec float64 `json:"duration_sec"`
	ThresholdDB float64 `json:"threshold_db"`
}

// LogSilenceStart appends a silence start event to the log file.
func LogSilenceStart(logPath string, duration, threshold float64) error {
	return appendLogEntry(logPath, SilenceLogEntry{
		Timestamp:   util.RFC3339Now(),
		Event:       "silence_start",
		DurationSec: duration,
		ThresholdDB: threshold,
	})
}

// LogSilenceEnd appends a silence end (recovery) event to the log file.
func LogSilenceEnd(logPath string, silenceDuration, threshold float64) error {
	return appendLogEntry(logPath, SilenceLogEntry{
		Timestamp:   util.RFC3339Now(),
		Event:       "silence_end",
		DurationSec: silenceDuration,
		ThresholdDB: threshold,
	})
}

// appendLogEntry appends a JSON log entry to the file.
func appendLogEntry(logPath string, entry SilenceLogEntry) error {
	if !util.IsConfigured(logPath) {
		return nil // Silently skip if not configured
	}

	jsonData, err := json.Marshal(entry)
	if err != nil {
		return util.WrapError("marshal log entry", err)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return util.WrapError("open log file", err)
	}
	defer util.SafeCloseFunc(f, "log file")()

	if _, err := f.Write(append(jsonData, '\n')); err != nil {
		return util.WrapError("write log entry", err)
	}

	return nil
}
