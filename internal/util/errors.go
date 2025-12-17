package util

import "fmt"

// WrapError wraps an error with a descriptive operation context.
func WrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to %s: %w", operation, err)
}
