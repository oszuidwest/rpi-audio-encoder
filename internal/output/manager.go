package output

import (
	"bytes"
	"context"
	"io"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
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
	retryDelay time.Duration
}

// NewManager creates a new output manager.
func NewManager() *Manager {
	return &Manager{
		processes: make(map[string]*Process),
	}
}

// Start starts an output FFmpeg process.
func (m *Manager) Start(output *types.Output, retryCount int, retryDelay time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if proc, exists := m.processes[output.ID]; exists && proc.running {
		return nil // Already running
	}

	args := BuildFFmpegArgs(output)

	log.Printf("Starting output %s: %s:%d", output.ID, output.Host, output.Port)

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
		retryDelay: retryDelay,
	}

	m.processes[output.ID] = proc

	if err := cmd.Start(); err != nil {
		cancel()
		if closeErr := stdinPipe.Close(); closeErr != nil {
			log.Printf("Failed to close stdin pipe: %v", closeErr)
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

	log.Printf("Stopping output %s", outputID)

	if stdin != nil {
		if err := stdin.Close(); err != nil {
			log.Printf("Failed to close stdin: %v", err)
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
			log.Printf("Failed to stop output %s: %v", id, err)
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
		log.Printf("Output write failed, marking as stopped: %v", err)
		proc.running = false
		if proc.stdin != nil {
			if closeErr := proc.stdin.Close(); closeErr != nil {
				log.Printf("Failed to close stdin: %v", closeErr)
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
func (m *Manager) GetProcess(outputID string) (cmd *exec.Cmd, retryCount int, retryDelay time.Duration, exists bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proc, exists := m.processes[outputID]
	if !exists {
		return nil, 0, 0, false
	}
	return proc.cmd, proc.retryCount, proc.retryDelay, true
}

// SetError sets the last error for an output.
func (m *Manager) SetError(outputID, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc, exists := m.processes[outputID]; exists {
		proc.lastError = errMsg
	}
}

// UpdateRetry updates retry state for an output.
func (m *Manager) UpdateRetry(outputID string, retryCount int, retryDelay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if proc, exists := m.processes[outputID]; exists {
		proc.retryCount = retryCount
		proc.retryDelay = retryDelay
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
