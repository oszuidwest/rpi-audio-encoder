package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"maps"
	"os/exec"
	"slices"
	"syscall"
	"time"
)

// startEnabledOutputs starts FFmpeg processes for all enabled outputs
// and starts the audio distributor
func (m *Encoder) startEnabledOutputs() {
	// Start the distributor that reads from source stdout and fans out to outputs
	go m.runDistributor()

	// Start all enabled outputs
	for output := range m.config.EnabledOutputs() {
		if err := m.StartOutput(output.ID); err != nil {
			log.Printf("Failed to start output %s: %v", output.ID, err)
		}
	}
}

// runDistributor reads audio from source FFmpeg stdout and distributes to all output processes
func (m *Encoder) runDistributor() {
	// Buffer size: 48000 Hz * 2 channels * 2 bytes * 0.1 sec = 19200 bytes (~100ms of audio)
	// Larger buffer reduces syscall overhead significantly compared to 4KB
	buf := make([]byte, 19200)
	for {
		// Get reader under lock and keep a reference
		m.mu.RLock()
		state := m.state
		reader := m.sourceStdout
		stopChan := m.stopChan
		m.mu.RUnlock()

		// Check if we should stop
		if state != StateRunning || reader == nil {
			return
		}

		// Check stop channel (non-blocking) for fast shutdown
		select {
		case <-stopChan:
			return
		default:
		}

		// Read from source stdout (blocking, but will return error when pipe closes)
		n, err := reader.Read(buf)
		if err != nil {
			// Source stopped or error - exit cleanly
			return
		}

		if n == 0 {
			continue
		}

		// Distribute to all running outputs under lock
		m.mu.Lock()
		for _, proc := range m.outputProcesses {
			if proc.running && proc.stdin != nil {
				if _, err := proc.stdin.Write(buf[:n]); err != nil {
					// Output died - mark as not running and close stdin
					// The runOutputProcess goroutine will handle restart logic
					log.Printf("Output write failed, marking as stopped: %v", err)
					proc.running = false
					if proc.stdin != nil {
						if err := proc.stdin.Close(); err != nil {
							log.Printf("Failed to close output stdin: %v", err)
						}
						proc.stdin = nil
					}
				}
			}
		}
		m.mu.Unlock()
	}
}

// stopAllOutputs stops all output processes
func (m *Encoder) stopAllOutputs() {
	m.mu.Lock()
	ids := slices.Collect(maps.Keys(m.outputProcesses))
	m.mu.Unlock()

	for _, id := range ids {
		if err := m.StopOutput(id); err != nil {
			log.Printf("Failed to stop output %s: %v", id, err)
		}
	}
}

// StartOutput starts an individual output FFmpeg process
func (m *Encoder) StartOutput(outputID string) error {
	m.mu.Lock()
	if m.state != StateRunning {
		m.mu.Unlock()
		return fmt.Errorf("encoder not running")
	}

	// Check if already running
	existingProc, exists := m.outputProcesses[outputID]
	if exists && existingProc.running {
		m.mu.Unlock()
		return nil
	}

	// Preserve retry state from existing process
	var retryCount int
	retryDelay := initialRetryDelay
	if exists {
		retryCount = existingProc.retryCount
		retryDelay = existingProc.retryDelay
	}
	m.mu.Unlock()

	// Get output config
	output := m.config.GetOutput(outputID)
	if output == nil {
		return fmt.Errorf("output not found: %s", outputID)
	}

	// Build output FFmpeg command using output's codec
	codecArgs := output.GetCodecArgs()
	format := output.GetOutputFormat()

	srtURL := fmt.Sprintf(
		"srt://%s:%d?pkt_size=1316&oheadbw=100&maxbw=-1&latency=10000000&mode=caller&transtype=live&streamid=%s&passphrase=%s",
		output.Host, output.Port, output.StreamID, output.Password,
	)

	// Use stdin (pipe:0) instead of named pipe - distributor will feed us data
	args := []string{
		"-f", "s16le",
		"-ar", "48000",
		"-ac", "2",
		"-hide_banner",
		"-loglevel", "warning",
		"-i", "pipe:0",
		"-codec:a",
	}
	args = append(args, codecArgs...)
	args = append(args, "-f", format, srtURL)

	log.Printf("Starting output %s: %s:%d", outputID, output.Host, output.Port)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// Create stdin pipe for receiving audio from distributor
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Capture stderr for error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	proc := &OutputProcess{
		cmd:        cmd,
		cancel:     cancel,
		stdin:      stdinPipe,
		running:    true,
		startTime:  time.Now(),
		retryCount: retryCount,
		retryDelay: retryDelay,
	}

	m.mu.Lock()
	m.outputProcesses[outputID] = proc
	m.mu.Unlock()

	// Start the process (not Run - we need to feed it via stdin)
	if err := cmd.Start(); err != nil {
		cancel()
		if closeErr := stdinPipe.Close(); closeErr != nil {
			log.Printf("Failed to close stdin pipe: %v", closeErr)
		}
		m.mu.Lock()
		delete(m.outputProcesses, outputID)
		m.mu.Unlock()
		return fmt.Errorf("failed to start output: %w", err)
	}

	// Wait for process in goroutine
	go m.runOutputProcess(outputID, cmd, &stderr)

	return nil
}

// runOutputProcess handles the output process lifecycle
func (m *Encoder) runOutputProcess(outputID string, cmd *exec.Cmd, stderr *bytes.Buffer) {
	startTime := time.Now()
	err := cmd.Wait() // Process already started, just wait for it
	runDuration := time.Since(startTime)

	m.mu.Lock()
	p, exists := m.outputProcesses[outputID]
	if !exists {
		m.mu.Unlock()
		return
	}

	p.running = false
	if err != nil {
		// Extract meaningful error from stderr
		errMsg := extractLastError(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		p.lastError = errMsg
		log.Printf("Output %s error: %s", outputID, errMsg)

		// Update retry state
		if runDuration >= successThreshold {
			p.retryCount = 0
			p.retryDelay = initialRetryDelay
		} else {
			p.retryCount++
			p.retryDelay *= 2
			if p.retryDelay > maxRetryDelay {
				p.retryDelay = maxRetryDelay
			}
		}
	} else {
		p.retryCount = 0
		p.retryDelay = initialRetryDelay
	}

	retryCount := p.retryCount
	retryDelay := p.retryDelay
	lastError := p.lastError
	state := m.state
	m.mu.Unlock()

	// Check if we should restart
	if state != StateRunning {
		m.removeOutput(outputID)
		return
	}

	output := m.config.GetOutput(outputID)
	if output == nil || !output.Enabled {
		m.removeOutput(outputID)
		return
	}

	// Check max retries
	if retryCount >= maxRetries {
		log.Printf("Output %s failed %d times, giving up: %s", outputID, maxRetries, lastError)
		m.removeOutput(outputID)
		return
	}

	log.Printf("Output %s stopped, waiting %v before restart (attempt %d/%d)...",
		outputID, retryDelay, retryCount+1, maxRetries)
	time.Sleep(retryDelay)

	// Re-check if output still exists and is enabled after sleep
	// (user might have deleted or disabled it during the wait)
	output = m.config.GetOutput(outputID)
	if output == nil || !output.Enabled {
		log.Printf("Output %s was removed or disabled during retry wait, not restarting", outputID)
		m.removeOutput(outputID)
		return
	}

	// Re-check encoder state after sleep
	m.mu.RLock()
	state = m.state
	m.mu.RUnlock()
	if state != StateRunning {
		m.removeOutput(outputID)
		return
	}

	if err := m.StartOutput(outputID); err != nil {
		log.Printf("Failed to restart output %s: %v", outputID, err)
		m.removeOutput(outputID)
	}
}

// StopOutput stops an individual output FFmpeg process with graceful shutdown
func (m *Encoder) StopOutput(outputID string) error {
	m.mu.Lock()
	proc, exists := m.outputProcesses[outputID]
	if !exists {
		m.mu.Unlock()
		return nil
	}

	// Check if already stopping or stopped
	if !proc.running {
		delete(m.outputProcesses, outputID)
		m.mu.Unlock()
		return nil
	}

	// Mark as not running to prevent distributor from writing
	proc.running = false

	// Get references while holding lock
	stdin := proc.stdin
	process := proc.cmd.Process
	cancel := proc.cancel
	proc.stdin = nil // Prevent distributor from using it
	m.mu.Unlock()

	log.Printf("Stopping output %s", outputID)

	// Close stdin first to signal EOF to ffmpeg
	if stdin != nil {
		if err := stdin.Close(); err != nil {
			log.Printf("Failed to close stdin for output %s: %v", outputID, err)
		}
	}

	// Send SIGINT for graceful shutdown (FFmpeg flushes buffers and closes connections)
	if process != nil {
		if err := process.Signal(syscall.SIGINT); err != nil {
			// Process might already be dead, use cancel as fallback
			if cancel != nil {
				cancel()
			}
		}
	}

	// Wait for process to exit with timeout
	// The runOutputProcess goroutine will handle cmd.Wait() and cleanup
	exited := pollUntil(func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		_, exists := m.outputProcesses[outputID]
		return !exists
	})

	select {
	case <-exited:
		log.Printf("Output %s stopped gracefully", outputID)
	case <-time.After(shutdownTimeout):
		log.Printf("Output %s did not stop in time, forcing kill", outputID)
		// Force kill via context cancellation (sends SIGKILL)
		if cancel != nil {
			cancel()
		}
		// Clean up from map
		m.removeOutput(outputID)
	}

	return nil
}

// removeOutput removes an output from the process map
func (m *Encoder) removeOutput(outputID string) {
	m.mu.Lock()
	delete(m.outputProcesses, outputID)
	m.mu.Unlock()
}

// GetAllOutputStatuses returns status for all tracked outputs
func (m *Encoder) GetAllOutputStatuses() map[string]OutputStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]OutputStatus)
	for id, proc := range m.outputProcesses {
		statuses[id] = OutputStatus{
			Running:    proc.running,
			LastError:  proc.lastError,
			RetryCount: proc.retryCount,
			MaxRetries: maxRetries,
			GivenUp:    proc.retryCount >= maxRetries,
		}
	}
	return statuses
}
