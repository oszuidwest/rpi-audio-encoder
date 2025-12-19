package output

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/ffmpeg"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// Output manages a single SRT output destination with automatic retry.
type Output struct {
	output      *types.Output
	ffmpegCmd   *exec.Cmd
	ffmpegStdin io.WriteCloser
	stderr      *util.BoundedBuffer
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
	getOutput      func() *types.Output
	encoderRunning func() bool
}

// NewOutput creates a new Output for the given output configuration.
func NewOutput(output *types.Output, getOutput func() *types.Output, encoderRunning func() bool) *Output {
	return &Output{
		output:         output,
		maxRetries:     output.GetMaxRetries(),
		backoff:        util.NewBackoff(types.InitialRetryDelay, types.MaxRetryDelay),
		stderr:         util.NewStderrBuffer(),
		getOutput:      getOutput,
		encoderRunning: encoderRunning,
	}
}

// Start begins streaming to the SRT output.
func (o *Output) Start() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.running {
		return nil // Already running
	}

	// Initialize channels
	o.stopChan = make(chan struct{})

	if err := o.startFFmpegLocked(); err != nil {
		o.lastError = err.Error()
		return err
	}

	o.running = true
	o.lastError = ""
	o.retryCount = 0
	o.backoff.Reset(types.InitialRetryDelay)

	// Start process monitor
	o.wg.Add(1)
	go o.monitorProcess()

	return nil
}

// Stop gracefully stops the output.
func (o *Output) Stop() error {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return nil
	}

	o.running = false

	// Signal stop to all goroutines
	close(o.stopChan)

	// Stop FFmpeg FIRST, before waiting for monitor goroutine.
	// This allows the monitor goroutine to exit (it's blocked on cmd.Wait()).
	_ = o.stopFFmpegLocked()
	o.mu.Unlock()

	// Wait for monitor goroutine to finish
	o.wg.Wait()

	return nil
}

// WriteAudio writes PCM audio data to the output.
func (o *Output) WriteAudio(data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.running || o.ffmpegStdin == nil {
		return nil
	}

	if _, err := o.ffmpegStdin.Write(data); err != nil {
		o.lastError = err.Error()
		slog.Warn("output write failed, marking as stopped", "output_id", o.output.ID, "error", err)
		// Don't trigger restart here - monitor goroutine will handle it
		return err
	}
	return nil
}

// IsRunning returns whether the output is active.
func (o *Output) IsRunning() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.running
}

// GetStatus returns the current output status.
func (o *Output) GetStatus(maxRetries int) types.OutputStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return types.OutputStatus{
		Running:    o.running,
		Stable:     o.running && time.Since(o.startTime) >= types.StableThreshold,
		LastError:  o.lastError,
		RetryCount: o.retryCount,
		MaxRetries: maxRetries,
		GivenUp:    o.retryCount > maxRetries,
	}
}

// monitorProcess monitors the FFmpeg process and handles restarts with backoff.
func (o *Output) monitorProcess() {
	defer o.wg.Done()

	for {
		// Get cmd reference under lock and keep it for Wait()
		o.mu.Lock()
		cmd := o.ffmpegCmd
		running := o.running
		if !running || cmd == nil {
			o.mu.Unlock()
			return
		}
		o.mu.Unlock()

		// Wait for process to exit (cmd reference is safe, process owns it)
		startTime := time.Now()
		err := cmd.Wait()
		runDuration := time.Since(startTime)

		// Check if we should stop
		select {
		case <-o.stopChan:
			return
		default:
		}

		o.mu.Lock()
		if !o.running {
			o.mu.Unlock()
			return
		}

		// Process exited unexpectedly
		if err != nil {
			errMsg := ffmpeg.ExtractLastError(o.stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			o.lastError = errMsg
			slog.Error("output error", "output_id", o.output.ID, "error", errMsg)

			// Update retry state with exponential backoff
			if runDuration >= types.SuccessThreshold {
				o.retryCount = 0
				o.backoff.Reset(types.InitialRetryDelay)
			}
		} else {
			o.retryCount = 0
			o.backoff.Reset(types.InitialRetryDelay)
		}

		// Clear stdin reference
		o.ffmpegStdin = nil
		o.ffmpegCmd = nil
		o.mu.Unlock()

		// Check if encoder is still running
		if !o.encoderRunning() {
			return
		}

		// Attempt restart with backoff
		if !o.attemptRestartWithBackoff() {
			return
		}
	}
}

// attemptRestartWithBackoff attempts to restart the output with exponential backoff.
// Returns false if we should stop trying (stopped or max retries exceeded).
func (o *Output) attemptRestartWithBackoff() bool {
	// Prevent concurrent restart attempts
	if !o.restarting.CompareAndSwap(false, true) {
		return true // Another restart in progress
	}
	defer o.restarting.Store(false)

	o.mu.Lock()
	o.retryCount++
	delay := o.backoff.Next()
	retryCount := o.retryCount
	maxRetries := o.maxRetries
	o.mu.Unlock()

	// Check if we've exceeded max retries
	if retryCount > maxRetries {
		slog.Warn("output gave up after retries", "output_id", o.output.ID, "retries", maxRetries)
		return false
	}

	slog.Info("output stopped, waiting before retry",
		"output_id", o.output.ID, "delay", delay, "retry", retryCount, "max_retries", maxRetries)

	// Wait with cancellation support
	select {
	case <-o.stopChan:
		return false
	case <-time.After(delay):
	}

	// Check if output was removed during wait
	out := o.getOutput()
	if out == nil {
		slog.Info("output was removed during retry wait, not restarting", "output_id", o.output.ID)
		return false
	}

	// Check if encoder is still running
	if !o.encoderRunning() {
		return false
	}

	// Check if still running
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.running {
		return false
	}

	// Update output config in case it changed
	o.output = out

	// Stop any existing process
	_ = o.stopFFmpegLocked()
	o.stderr.Reset()

	// Try to start again
	if err := o.startFFmpegLocked(); err != nil {
		o.lastError = err.Error()
		slog.Error("failed to restart output", "output_id", o.output.ID, "error", err, "retry", o.retryCount)
		return true // Keep trying
	}

	o.lastError = ""
	slog.Info("output restarted successfully", "output_id", o.output.ID, "retry", o.retryCount)
	return true
}

// startFFmpegLocked starts the FFmpeg process for streaming.
// Caller must hold o.mu.
func (o *Output) startFFmpegLocked() error {
	args := BuildFFmpegArgs(o.output)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}

	// Capture stderr for error reporting (bounded buffer)
	o.stderr.Reset()
	cmd.Stderr = o.stderr

	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdinPipe.Close()
		return err
	}

	o.ffmpegCmd = cmd
	o.ffmpegStdin = stdinPipe
	o.startTime = time.Now()

	// Store cancel for stopFFmpegLocked
	o.ffmpegCmd.Cancel = func() error {
		cancel()
		return nil
	}

	slog.Info("starting output", "output_id", o.output.ID, "host", o.output.Host, "port", o.output.Port)
	return nil
}

// stopFFmpegLocked stops the current FFmpeg process.
// Caller must hold o.mu.
func (o *Output) stopFFmpegLocked() error {
	if o.ffmpegStdin != nil {
		if err := o.ffmpegStdin.Close(); err != nil {
			slog.Warn("failed to close output stdin", "output_id", o.output.ID, "error", err)
		}
		o.ffmpegStdin = nil
	}

	if o.ffmpegCmd != nil && o.ffmpegCmd.Process != nil {
		// Request graceful shutdown via SIGINT
		if err := o.ffmpegCmd.Process.Signal(syscall.SIGINT); err != nil {
			// Force kill if signal fails
			if o.ffmpegCmd.Cancel != nil {
				_ = o.ffmpegCmd.Cancel()
			}
		}
		// Note: monitorProcess will handle Wait() - no blocking sleep here
	}

	o.ffmpegCmd = nil
	return nil
}

// Manager coordinates multiple output destinations.
type Manager struct {
	outputs map[string]*Output
	mu      sync.RWMutex
}

// NewManager creates a new output manager.
func NewManager() *Manager {
	return &Manager{
		outputs: make(map[string]*Output),
	}
}

// Start starts an output destination.
func (m *Manager) Start(output *types.Output, getOutput func() *types.Output, encoderRunning func() bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If already exists and running, do nothing
	if out, exists := m.outputs[output.ID]; exists && out.IsRunning() {
		return nil
	}

	// Create new output
	out := NewOutput(output, getOutput, encoderRunning)
	if err := out.Start(); err != nil {
		return err
	}

	m.outputs[output.ID] = out
	return nil
}

// Stop stops an output by ID.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	out, exists := m.outputs[id]
	if !exists {
		return nil
	}

	if err := out.Stop(); err != nil {
		slog.Warn("error stopping output", "output_id", id, "error", err)
	}

	delete(m.outputs, id)
	slog.Info("output stopped", "output_id", id)
	return nil
}

// StopAll stops all outputs.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, out := range m.outputs {
		if err := out.Stop(); err != nil {
			slog.Warn("error stopping output", "output_id", id, "error", err)
		}
	}
	m.outputs = make(map[string]*Output)
}

// WriteAudio writes PCM data to a specific output.
func (m *Manager) WriteAudio(id string, data []byte) error {
	m.mu.RLock()
	out, exists := m.outputs[id]
	m.mu.RUnlock()

	if !exists || !out.IsRunning() {
		return nil
	}

	return out.WriteAudio(data)
}

// GetAllStatuses returns status for all tracked outputs.
func (m *Manager) GetAllStatuses(getMaxRetries func(string) int) map[string]types.OutputStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]types.OutputStatus, len(m.outputs))
	for id, out := range m.outputs {
		maxRetries := getMaxRetries(id)
		statuses[id] = out.GetStatus(maxRetries)
	}
	return statuses
}
