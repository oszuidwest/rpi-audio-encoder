package util

import "time"

// RFC3339Now returns the current UTC time formatted as RFC3339.
func RFC3339Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
