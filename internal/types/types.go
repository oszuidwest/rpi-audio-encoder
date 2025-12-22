// Package types provides shared type definitions used across the encoder.
package types

import (
	"context"
	"io"
	"os/exec"
	"time"
)

// EncoderState represents the current state of the encoder.
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

// Retry settings.
const (
	InitialRetryDelay = 3 * time.Second
	MaxRetryDelay     = 60 * time.Second
	MaxRetries        = 10
	SuccessThreshold  = 30 * time.Second // Reset retry count after running this long
	StableThreshold   = 10 * time.Second // Consider connection stable after this duration
)

// Shutdown settings.
const (
	ShutdownTimeout = 3 * time.Second       // Time to wait for graceful shutdown before SIGKILL
	PollInterval    = 50 * time.Millisecond // Interval for polling process state
)

// Output represents a single SRT output destination.
type Output struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Password   string `json:"password"`
	StreamID   string `json:"streamid"`
	Codec      string `json:"codec"`
	MaxRetries int    `json:"max_retries,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// DefaultMaxRetries is the default number of retry attempts for outputs.
const DefaultMaxRetries = 99

// GetMaxRetries returns the configured max retries or the default value.
func (o *Output) GetMaxRetries() int {
	if o.MaxRetries <= 0 {
		return DefaultMaxRetries
	}
	return o.MaxRetries
}

// CodecPreset defines FFmpeg encoding parameters for a codec.
type CodecPreset struct {
	Args   []string
	Format string
}

// CodecPresets maps codec names to their FFmpeg configuration.
var CodecPresets = map[string]CodecPreset{
	"mp2": {[]string{"libtwolame", "-b:a", "384k", "-psymodel", "4"}, "mp2"},
	"mp3": {[]string{"libmp3lame", "-b:a", "320k"}, "mp3"},
	"ogg": {[]string{"libvorbis", "-qscale:a", "10"}, "ogg"},
	"wav": {[]string{"pcm_s16le"}, "matroska"},
}

// DefaultCodec is used when an unknown codec is specified.
const DefaultCodec = "mp3"

// GetCodecArgs returns FFmpeg codec arguments for this output's codec.
func (o *Output) GetCodecArgs() []string {
	if preset, ok := CodecPresets[o.Codec]; ok {
		return preset.Args
	}
	return CodecPresets[DefaultCodec].Args
}

// GetOutputFormat returns the FFmpeg output format for this output's codec.
func (o *Output) GetOutputFormat() string {
	if preset, ok := CodecPresets[o.Codec]; ok {
		return preset.Format
	}
	return CodecPresets[DefaultCodec].Format
}

// OutputProcess tracks an individual output FFmpeg process.
type OutputProcess struct {
	Cmd        *exec.Cmd
	Cancel     context.CancelFunc
	Stdin      io.WriteCloser // Audio data input
	Running    bool
	LastError  string
	StartTime  time.Time
	RetryCount int
	RetryDelay time.Duration
}

// OutputStatus contains runtime status for an output.
type OutputStatus struct {
	Running    bool   `json:"running"`
	Stable     bool   `json:"stable,omitzero"`
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

// SilenceLevel represents the silence detection state.
type SilenceLevel string

const (
	SilenceLevelNone   SilenceLevel = ""       // No silence detected
	SilenceLevelActive SilenceLevel = "active" // Silence confirmed (duration threshold exceeded)
)

// AudioLevels contains current audio level measurements.
type AudioLevels struct {
	Left            float64      `json:"left"`                      // RMS level in dB (-60 to 0)
	Right           float64      `json:"right"`                     // RMS level in dB
	PeakLeft        float64      `json:"peak_left"`                 // Peak level in dB
	PeakRight       float64      `json:"peak_right"`                // Peak level in dB
	Silence         bool         `json:"silence,omitzero"`          // True if audio below threshold
	SilenceDuration float64      `json:"silence_duration,omitzero"` // Silence duration in seconds
	SilenceLevel    SilenceLevel `json:"silence_level,omitzero"`    // "active" when in confirmed silence state
	ClipLeft        int          `json:"clip_left,omitzero"`        // Clipped samples on left channel
	ClipRight       int          `json:"clip_right,omitzero"`       // Clipped samples on right channel
}
