// Package util provides shared utility functions used across the encoder.
package util

import (
	"io"
	"log"
)

// SafeClose closes an io.Closer and logs any error that occurs.
// It safely handles nil closers.
func SafeClose(closer io.Closer, name string) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		log.Printf("Failed to close %s: %v", name, err)
	}
}

// SafeCloseFunc creates a defer-friendly closure for resource cleanup.
func SafeCloseFunc(closer io.Closer, name string) func() {
	return func() {
		SafeClose(closer, name)
	}
}
