package util

import (
	"fmt"
	"strings"
)

// WrapError wraps an error with a descriptive operation context.
func WrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to %s: %w", operation, err)
}

// ExtractLastError returns the last meaningful line from FFmpeg stderr output.
// Truncates lines longer than 200 characters.
func ExtractLastError(stderr string) string {
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
