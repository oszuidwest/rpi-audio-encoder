package util

import "log/slog"

// NotifyResultf executes a notification function and logs the result with formatted message.
// Errors are logged internally, so no error is returned.
func NotifyResultf(fn func() error, notifyType string, enabled bool, format string, args ...interface{}) {
	err := fn()
	if err != nil {
		slog.Error("notification failed", "type", notifyType, "error", err)
	} else if enabled {
		slog.Info("notification sent", "type", notifyType)
	}
}
