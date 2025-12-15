package main

import (
	"strings"
	"time"
)

// pollUntil polls a condition function until it returns true, then closes the returned channel
func pollUntil(condition func() bool) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for !condition() {
			time.Sleep(pollInterval)
		}
		close(done)
	}()
	return done
}

// extractLastError extracts the last meaningful error line from FFmpeg stderr
func extractLastError(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			if len(line) > 200 {
				return line[:200] + "..."
			}
			return line
		}
	}
	return ""
}
