package main

import (
	"strings"
	"time"
)

// pollUntil signals when the given condition becomes true.
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

// extractLastError returns the last meaningful error from FFmpeg output.
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
