package util

import "time"

// Backoff calculates exponential backoff delays with a configurable factor and maximum.
type Backoff struct {
	current time.Duration
	initial time.Duration
	max     time.Duration
	factor  float64
}

// NewBackoff creates a backoff calculator with sensible defaults.
// The factor defaults to 2.0 (doubling) for exponential backoff.
func NewBackoff(initial, max time.Duration) *Backoff {
	return &Backoff{
		current: initial,
		initial: initial,
		max:     max,
		factor:  2.0,
	}
}

// Next returns the current delay and advances to the next delay value.
// It doubles the delay (by default factor of 2.0) up to the maximum.
func (b *Backoff) Next() time.Duration {
	current := b.current
	b.current = time.Duration(float64(b.current) * b.factor)
	if b.current > b.max {
		b.current = b.max
	}
	return current
}

// Current returns the current delay without advancing.
func (b *Backoff) Current() time.Duration {
	return b.current
}

// Reset returns the backoff to the initial delay.
func (b *Backoff) Reset(initial time.Duration) {
	b.current = initial
	b.initial = initial
}
