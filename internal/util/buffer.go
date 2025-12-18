package util

import (
	"sync"

	"github.com/oszuidwest/zwfm-encoder/internal/ffmpeg"
)

// BoundedBuffer is a thread-safe buffer with a maximum size.
// When the buffer exceeds maxSize, older data is discarded.
type BoundedBuffer struct {
	data    []byte
	maxSize int
	mu      sync.Mutex
}

// NewBoundedBuffer creates a new bounded buffer with the specified max size.
func NewBoundedBuffer(maxSize int) *BoundedBuffer {
	return &BoundedBuffer{
		data:    make([]byte, 0, maxSize),
		maxSize: maxSize,
	}
}

// Write implements io.Writer. If adding data would exceed maxSize,
// older data is discarded to make room.
func (b *BoundedBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n = len(p)

	// If incoming data alone exceeds max, just keep the tail
	if n >= b.maxSize {
		b.data = make([]byte, b.maxSize)
		copy(b.data, p[n-b.maxSize:])
		return n, nil
	}

	// Calculate how much space we need
	newLen := len(b.data) + n
	if newLen > b.maxSize {
		// Discard oldest data
		discard := newLen - b.maxSize
		b.data = b.data[discard:]
	}

	b.data = append(b.data, p...)
	return n, nil
}

// String returns the buffer contents as a string.
func (b *BoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// Reset clears the buffer.
func (b *BoundedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = b.data[:0]
}

// NewStderrBuffer creates a bounded buffer sized for FFmpeg stderr output.
func NewStderrBuffer() *BoundedBuffer {
	return NewBoundedBuffer(ffmpeg.MaxStderrSize)
}
