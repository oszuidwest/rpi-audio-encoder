package util

import "log"

// NotifyResultf executes a notification function and logs the result with formatted message.
// Errors are logged internally, so no error is returned.
func NotifyResultf(fn func() error, notifyType string, enabled bool, format string, args ...interface{}) {
	err := fn()
	if err != nil {
		log.Printf("%s failed: %v", notifyType, err)
	} else if enabled {
		log.Printf("%s sent successfully "+format, append([]interface{}{notifyType}, args...)...)
	}
}
