package recording

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/ffmpeg"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// Recorder manages a single recording destination with hourly file rotation.
type Recorder struct {
	recording   *types.Recording
	ffmpegCmd   *exec.Cmd
	ffmpegStdin io.WriteCloser
	stderr      *util.BoundedBuffer
	currentFile string
	currentHour int
	currentDay  int
	startTime   time.Time

	// Synchronization
	mu       sync.RWMutex
	running  bool
	stopChan chan struct{}
	wg       sync.WaitGroup

	// Error tracking with backoff
	lastError  string
	retryCount int
	maxRetries int
	backoff    *util.Backoff
	restarting atomic.Bool

	// Dependencies for retry logic
	getRecording   func() *types.Recording
	encoderRunning func() bool
}

// NewRecorder creates a new Recorder for the given recording configuration.
func NewRecorder(recording *types.Recording, getRecording func() *types.Recording, encoderRunning func() bool) *Recorder {
	return &Recorder{
		recording:      recording,
		maxRetries:     recording.GetMaxRetries(),
		backoff:        util.NewBackoff(types.InitialRetryDelay, types.MaxRetryDelay),
		stderr:         util.NewStderrBuffer(),
		getRecording:   getRecording,
		encoderRunning: encoderRunning,
	}
}

// Start begins recording audio to files.
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return nil // Already running
	}

	// Initialize channels
	r.stopChan = make(chan struct{})

	if err := r.startFFmpegLocked(); err != nil {
		r.lastError = err.Error()
		return err
	}

	r.running = true
	r.lastError = ""
	r.retryCount = 0
	r.backoff.Reset(types.InitialRetryDelay)

	// Start process monitor
	r.wg.Add(1)
	go r.monitorProcess()

	// Note: logging is done in startFFmpegLocked to avoid duplicate logs
	return nil
}

// Stop gracefully stops recording.
func (r *Recorder) Stop() error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}

	r.running = false

	// Signal stop to all goroutines
	close(r.stopChan)

	// Stop FFmpeg FIRST, before waiting for monitor goroutine.
	// This allows the monitor goroutine to exit (it's blocked on cmd.Wait()).
	_ = r.stopFFmpegLocked()
	r.mu.Unlock()

	// Wait for monitor goroutine to finish
	r.wg.Wait()

	return nil
}

// WriteAudio writes PCM audio data to the recorder.
// For auto mode, it handles hourly file rotation automatically.
// For manual mode, no rotation occurs.
func (r *Recorder) WriteAudio(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running || r.ffmpegStdin == nil {
		return nil
	}

	// Check if rotation is needed (new hour) - only for auto mode
	if r.recording.IsAuto() {
		now := time.Now()
		if now.Hour() != r.currentHour || now.Day() != r.currentDay {
			if err := r.rotateLocked(); err != nil {
				slog.Error("failed to rotate recording file", "recording_id", r.recording.ID, "error", err)
				// Continue with existing file rather than stopping
			}
		}
	}

	if _, err := r.ffmpegStdin.Write(data); err != nil {
		r.lastError = err.Error()
		slog.Warn("recording write error", "recording_id", r.recording.ID, "error", err)
		// Don't trigger restart here - monitor goroutine will handle it
		return err
	}
	return nil
}

// IsRunning returns whether the recorder is active.
func (r *Recorder) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// GetStatus returns the current recording status.
func (r *Recorder) GetStatus() *types.RecordingStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var startTimeStr string
	if !r.startTime.IsZero() {
		startTimeStr = r.startTime.Format(time.RFC3339)
	}

	return &types.RecordingStatus{
		Running:     r.running,
		CurrentFile: r.currentFile,
		StartTime:   startTimeStr,
		LastError:   r.lastError,
		RetryCount:  r.retryCount,
	}
}

// monitorProcess monitors the FFmpeg process and handles restarts with backoff.
func (r *Recorder) monitorProcess() {
	defer r.wg.Done()

	for {
		// Get cmd reference under lock and keep it for Wait()
		r.mu.Lock()
		cmd := r.ffmpegCmd
		running := r.running
		if !running || cmd == nil {
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()

		// Wait for process to exit (cmd reference is safe, process owns it)
		err := cmd.Wait()

		// Check if we should stop
		select {
		case <-r.stopChan:
			return
		default:
		}

		r.mu.Lock()
		if !r.running {
			r.mu.Unlock()
			return
		}

		// Process exited unexpectedly
		if err != nil {
			errMsg := ffmpeg.ExtractLastError(r.stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			r.lastError = errMsg
			slog.Error("recording process error", "recording_id", r.recording.ID, "error", errMsg)
		}

		// Clear stdin reference
		r.ffmpegStdin = nil
		r.ffmpegCmd = nil
		r.mu.Unlock()

		// Check if encoder is still running
		if !r.encoderRunning() {
			return
		}

		// Attempt restart with backoff
		if !r.attemptRestartWithBackoff() {
			return
		}
	}
}

// attemptRestartWithBackoff attempts to restart recording with exponential backoff.
// Returns false if we should stop trying (stopped or max retries exceeded).
func (r *Recorder) attemptRestartWithBackoff() bool {
	// Prevent concurrent restart attempts
	if !r.restarting.CompareAndSwap(false, true) {
		return true // Another restart in progress
	}
	defer r.restarting.Store(false)

	r.mu.Lock()
	r.retryCount++
	delay := r.backoff.Next()
	retryCount := r.retryCount
	maxRetries := r.maxRetries
	r.mu.Unlock()

	// Check if we've exceeded max retries
	if retryCount > maxRetries {
		slog.Warn("recording gave up after retries", "recording_id", r.recording.ID, "retries", maxRetries)
		return false
	}

	slog.Info("recording stopped, waiting before retry",
		"recording_id", r.recording.ID, "delay", delay, "retry", retryCount, "max_retries", maxRetries)

	// Wait with cancellation support
	select {
	case <-r.stopChan:
		return false
	case <-time.After(delay):
	}

	// Check if recording was removed during wait
	rec := r.getRecording()
	if rec == nil {
		slog.Info("recording was removed during retry wait, not restarting", "recording_id", r.recording.ID)
		return false
	}

	// Check if encoder is still running
	if !r.encoderRunning() {
		return false
	}

	// Check if still running
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return false
	}

	// Update recording config in case it changed
	r.recording = rec

	// Stop any existing process
	_ = r.stopFFmpegLocked()
	r.stderr.Reset()

	// Try to start again
	if err := r.startFFmpegLocked(); err != nil {
		r.lastError = err.Error()
		slog.Error("failed to restart recording", "recording_id", r.recording.ID, "error", err, "retry", r.retryCount)
		return true // Keep trying
	}

	r.lastError = ""
	slog.Info("recording restarted successfully", "recording_id", r.recording.ID, "retry", r.retryCount)
	return true
}

// startFFmpegLocked starts the FFmpeg process for recording.
// Caller must hold r.mu.
func (r *Recorder) startFFmpegLocked() error {
	now := time.Now()
	outputPath := r.buildFilePath(now)

	// Ensure directory exists
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create recording directory: %w", err)
	}

	args := BuildFFmpegArgs(outputPath, r.recording.Codec)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Capture stderr for error reporting (bounded buffer)
	r.stderr.Reset()
	cmd.Stderr = r.stderr

	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdinPipe.Close()
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	r.ffmpegCmd = cmd
	r.ffmpegStdin = stdinPipe
	r.currentFile = outputPath
	r.currentHour = now.Hour()
	r.currentDay = now.Day()
	r.startTime = now

	// Store cancel for stopFFmpegLocked
	r.ffmpegCmd.Cancel = func() error {
		cancel()
		return nil
	}

	slog.Info("recording to file", "recording_id", r.recording.ID, "file", outputPath, "codec", r.recording.Codec)
	return nil
}

// stopFFmpegLocked stops the current FFmpeg process.
// Caller must hold r.mu.
func (r *Recorder) stopFFmpegLocked() error {
	if r.ffmpegStdin != nil {
		if err := r.ffmpegStdin.Close(); err != nil {
			slog.Warn("failed to close recording stdin", "recording_id", r.recording.ID, "error", err)
		}
		r.ffmpegStdin = nil
	}

	if r.ffmpegCmd != nil && r.ffmpegCmd.Process != nil {
		// Request graceful shutdown via SIGINT
		if err := r.ffmpegCmd.Process.Signal(syscall.SIGINT); err != nil {
			// Force kill if signal fails
			if r.ffmpegCmd.Cancel != nil {
				_ = r.ffmpegCmd.Cancel()
			}
		}
		// Note: monitorProcess will handle Wait() - no blocking sleep here
	}

	r.ffmpegCmd = nil
	return nil
}

// rotateLocked closes the current file and starts a new one.
// Caller must hold r.mu.
func (r *Recorder) rotateLocked() error {
	slog.Info("rotating recording file", "recording_id", r.recording.ID)

	// Stop current FFmpeg
	if err := r.stopFFmpegLocked(); err != nil {
		slog.Warn("error stopping FFmpeg during rotation", "recording_id", r.recording.ID, "error", err)
	}

	// Reset stderr buffer
	r.stderr.Reset()

	// Start new FFmpeg with new file
	return r.startFFmpegLocked()
}

// buildFilePath constructs the output file path for the given time.
// Format: {path}/{YYYY-MM-DD}/{HH-00}.{ext}
// If the file already exists (e.g., after a mid-hour reboot), appends _1, _2, etc.
func (r *Recorder) buildFilePath(t time.Time) string {
	ext := types.GetFileExtension(r.recording.Codec)
	dateDir := t.Format("2006-01-02")
	dir := filepath.Join(r.recording.Path, dateDir)

	// Base filename without extension
	baseName := fmt.Sprintf("%02d-00", t.Hour())
	basePath := filepath.Join(dir, baseName+"."+ext)

	// If file doesn't exist, use base path
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return basePath
	}

	// File exists, find unique suffix
	for i := 1; i < 100; i++ {
		suffixedPath := filepath.Join(dir, fmt.Sprintf("%s_%d.%s", baseName, i, ext))
		if _, err := os.Stat(suffixedPath); os.IsNotExist(err) {
			return suffixedPath
		}
	}

	// Fallback (should never happen): use timestamp
	return filepath.Join(dir, fmt.Sprintf("%s_%d.%s", baseName, t.Unix(), ext))
}
