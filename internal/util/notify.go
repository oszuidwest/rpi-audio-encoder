package util

import "log/slog"

// LogNotifyResult executes a notification function and logs the result.
func LogNotifyResult(fn func() error, notifyType string, enabled bool) {
	err := fn()
	if err != nil {
		slog.Error("notification failed", "type", notifyType, "error", err)
	} else if enabled {
		slog.Info("notification sent", "type", notifyType)
	}
}
