package output

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// Manager manages multiple output FFmpeg processes.
type Manager struct {
	processes map[string]*Process
	mu        sync.RWMutex
}

// Process tracks an individual output FFmpeg process.
type Process struct {
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	stdin      io.WriteCloser
	running    bool
	lastError  string
	startTime  time.Time
	retryCount int
	backoff    *util.Backoff
}

// NewManager creates a new output manager.
func NewManager() *Manager {
	return &Manager{
		processes: make(map[string]*Process),
	}
}

// Start starts an output FFmpeg process.
// If a process entry already exists, retry state (count and backoff) is preserved.
func (m *Manager) Start(output *types.Output) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.processes[output.ID]
	if exists && existing.running {
		return nil // Already running
	}

	// Preserve retry state from existing entry, or create fresh
	var retryCount int
	var backoff *util.Backoff
	if exists && existing.backoff != nil {
		retryCount = existing.retryCount
		backoff = existing.backoff
	} else {
		retryCount = 0
		backoff = util.NewBackoff(types.InitialRetryDelay, types.MaxRetryDelay)
	}

	args := BuildFFmpegArgs(output)

	slog.Info("starting output", "output_id", output.ID, "host", output.Host, "port", output.Port)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	proc := &Process{
		cmd:        cmd,
		cancel:     cancel,
		stdin:      stdinPipe,
		running:    true,
		startTime:  time.Now(),
		retryCount: retryCount,
		backoff:    backoff,
	}

	m.processes[output.ID] = proc

	if err := cmd.Start(); err != nil {
		cancel()
		if closeErr := stdinPipe.Close(); closeErr != nil {
			slog.Warn("failed to close stdin pipe", "error", closeErr)
		}
		delete(m.processes, output.ID)
		return err
	}

	return nil
}

// Stop stops an output with graceful shutdown.
func (m *Manager) Stop(outputID string) error {
	m.mu.Lock()
	proc, exists := m.processes[outputID]
	if !exists {
		m.mu.Unlock()
		return nil
	}

	if !proc.running {
		delete(m.processes, outputID)
		m.mu.Unlock()
		return nil
	}

	proc.running = false
	stdin := proc.stdin
	process := proc.cmd.Process
	cancel := proc.cancel
	proc.stdin = nil
	m.mu.Unlock()

	slog.Info("stopping output", "output_id", outputID)

	if stdin != nil {
		if err := stdin.Close(); err != nil {
			slog.Warn("failed to close stdin", "error", err)
		}
	}

	// Request graceful shutdown
	if process != nil {
		if err := process.Signal(syscall.SIGINT); err != nil && cancel != nil {
			cancel()
		}
	}

	// Wait briefly for graceful exit
	time.Sleep(100 * time.Millisecond)

	m.mu.Lock()
	delete(m.processes, outputID)
	m.mu.Unlock()

	return nil
}

// StopAll stops all outputs.
func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		if err := m.Stop(id); err != nil {
			slog.Error("failed to stop output", "output_id", id, "error", err)
		}
	}
}

// WriteAudio writes audio data to a specific output.
func (m *Manager) WriteAudio(outputID string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	proc, exists := m.processes[outputID]
	if !exists || !proc.running || proc.stdin == nil {
		return nil
	}

	if _, err := proc.stdin.Write(data); err != nil {
		slog.Warn("output write failed, marking as stopped", "error", err)
		proc.running = false
		if proc.stdin != nil {
			if closeErr := proc.stdin.Close(); closeErr != nil {
				slog.Warn("failed to close stdin", "error", closeErr)
			}
			proc.stdin = nil
		}
		return err
	}
	return nil
}

// GetAllStatuses returns status for all tracked outputs.
func (m *Manager) GetAllStatuses(getMaxRetries func(string) int) map[string]types.OutputStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]types.OutputStatus)
	for id, proc := range m.processes {
		maxRetries := getMaxRetries(id)
		statuses[id] = types.OutputStatus{
			Running:    proc.running,
			Stable:     proc.running && time.Since(proc.startTime) >= types.StableThreshold,
			LastError:  proc.lastError,
			RetryCount: proc.retryCount,
			MaxRetries: maxRetries,
			GivenUp:    proc.retryCount > maxRetries,
		}
	}
	return statuses
}

// GetProcess returns process info for monitoring.
// The returned backoff pointer is safe to use directly since only MonitorAndRetry accesses it.
func (m *Manager) GetProcess(outputID string) (cmd *exec.Cmd, retryCount int, backoff *util.Backoff, exists bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proc, exists := m.processes[outputID]
	if !exists {
		return nil, 0, nil, false
	}
	return proc.cmd, proc.retryCount, proc.backoff, true
}

// SetError sets the last error for an output.
func (m *Manager) SetError(outputID, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc, exists := m.processes[outputID]; exists {
		proc.lastError = errMsg
	}
}

// IncrementRetry increments the retry count for an output.
func (m *Manager) IncrementRetry(outputID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc, exists := m.processes[outputID]; exists {
		proc.retryCount++
	}
}

// ResetRetry resets the retry count and backoff for an output.
func (m *Manager) ResetRetry(outputID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc, exists := m.processes[outputID]; exists {
		proc.retryCount = 0
		if proc.backoff != nil {
			proc.backoff.Reset(types.InitialRetryDelay)
		}
	}
}

// MarkStopped marks an output as not running.
func (m *Manager) MarkStopped(outputID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc, exists := m.processes[outputID]; exists {
		proc.running = false
	}
}

// Remove removes an output from tracking.
func (m *Manager) Remove(outputID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processes, outputID)
}

// MonitorAndRetry monitors an output process and handles retry logic with exponential backoff.
// It runs in a loop until the output is stopped, removed, or exceeds max retries.
func (m *Manager) MonitorAndRetry(outputID string, getOutput func() *types.Output, stopChan <-chan struct{}, encoderRunning func() bool) {
	for {
		// Check if we should stop
		select {
		case <-stopChan:
			m.Remove(outputID)
			return
		default:
		}

		// Get the process to monitor (backoff is stored in Process, no recreation needed)
		cmd, retryCount, backoff, exists := m.GetProcess(outputID)
		if !exists || cmd == nil || backoff == nil {
			return
		}

		// Wait for the process to exit
		startTime := time.Now()
		err := cmd.Wait()
		runDuration := time.Since(startTime)

		// Mark as stopped
		m.MarkStopped(outputID)

		if err != nil {
			// Extract error message from stderr
			var errMsg string
			if stderr, ok := cmd.Stderr.(*bytes.Buffer); ok && stderr != nil {
				errMsg = extractLastError(stderr.String())
			}
			if errMsg == "" {
				errMsg = err.Error()
			}
			m.SetError(outputID, errMsg)
			slog.Error("output error", "output_id", outputID, "error", errMsg)

			// Update retry state with exponential backoff
			if runDuration >= types.SuccessThreshold {
				m.ResetRetry(outputID)
				retryCount = 0
			} else {
				m.IncrementRetry(outputID)
				retryCount++
				backoff.Next() // Advance to next delay
			}
		} else {
			m.ResetRetry(outputID)
			retryCount = 0
		}

		// Check if encoder is still running
		if !encoderRunning() {
			m.Remove(outputID)
			return
		}

		// Get current output config
		out := getOutput()
		if out == nil {
			m.Remove(outputID)
			return
		}

		// Check if we've exceeded max retries
		maxRetries := out.GetMaxRetries()
		if retryCount > maxRetries {
			slog.Warn("output gave up after retries", "output_id", outputID, "retries", maxRetries)
			return // Keep in processes for status reporting
		}

		// Get current delay from backoff (already advanced if error occurred)
		retryDelay := backoff.Current()

		// Wait before retrying
		slog.Info("output stopped, waiting before retry",
			"output_id", outputID, "delay", retryDelay, "retry", retryCount, "max_retries", maxRetries)

		select {
		case <-stopChan:
			m.Remove(outputID)
			return
		case <-time.After(retryDelay):
			// Proceed to retry
		}

		// Verify output wasn't removed during wait
		out = getOutput()
		if out == nil {
			slog.Info("output was removed during retry wait, not restarting", "output_id", outputID)
			m.Remove(outputID)
			return
		}

		// Verify encoder is still running
		if !encoderRunning() {
			m.Remove(outputID)
			return
		}

		// Start the output (retry state is preserved automatically)
		if err := m.Start(out); err != nil {
			slog.Error("failed to restart output", "output_id", outputID, "error", err)
			m.Remove(outputID)
			return
		}
	}
}

// extractLastError extracts the last meaningful error from FFmpeg stderr.
func extractLastError(stderr string) string {
	if stderr == "" {
		return ""
	}
	lines := []string{}
	for _, line := range bytes.Split([]byte(stderr), []byte("\n")) {
		lines = append(lines, string(line))
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := string(bytes.TrimSpace([]byte(lines[i])))
		if line != "" {
			if len(line) > 200 {
				return line[:200] + "..."
			}
			return line
		}
	}
	return ""
}
