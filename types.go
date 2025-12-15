package main

import (
	"context"
	"io"
	"os/exec"
	"time"
)

// EncoderState represents the current state of the encoder
type EncoderState string

const (
	// StateStopped indicates the encoder is not running.
	StateStopped EncoderState = "stopped"
	// StateStarting indicates the encoder is initializing.
	StateStarting EncoderState = "starting"
	// StateRunning indicates the encoder is actively processing audio.
	StateRunning EncoderState = "running"
	// StateStopping indicates the encoder is shutting down.
	StateStopping EncoderState = "stopping"
)

// Retry settings
const (
	initialRetryDelay = 3 * time.Second
	maxRetryDelay     = 60 * time.Second
	maxRetries        = 10
	successThreshold  = 30 * time.Second // Reset retry count after running this long
)

// Shutdown settings
const (
	shutdownTimeout = 3 * time.Second      // Time to wait for graceful shutdown before SIGKILL
	pollInterval    = 50 * time.Millisecond // Interval for polling process state
)

// OutputProcess tracks an individual output FFmpeg process
type OutputProcess struct {
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	stdin      io.WriteCloser // Stdin pipe for receiving audio data
	running    bool
	lastError  string
	startTime  time.Time
	retryCount int
	retryDelay time.Duration
}

// OutputStatus contains runtime status for an output
type OutputStatus struct {
	Running    bool   `json:"running"`
	LastError  string `json:"last_error,omitzero"`
	RetryCount int    `json:"retry_count,omitzero"`
	MaxRetries int    `json:"max_retries"`
	GivenUp    bool   `json:"given_up,omitzero"`
}

// EncoderStatus contains a summary of the encoder's current operational state.
type EncoderStatus struct {
	State            EncoderState `json:"state"`
	Uptime           string       `json:"uptime,omitzero"`
	LastError        string       `json:"last_error,omitzero"`
	OutputCount      int          `json:"output_count"`
	SourceRetryCount int          `json:"source_retry_count,omitzero"`
	SourceMaxRetries int          `json:"source_max_retries"`
}

// AudioLevels contains current audio level measurements
type AudioLevels struct {
	Left      float64 `json:"left"`       // RMS level in dB (-60 to 0)
	Right     float64 `json:"right"`      // RMS level in dB
	PeakLeft  float64 `json:"peak_left"`  // Peak level in dB
	PeakRight float64 `json:"peak_right"` // Peak level in dB
}
