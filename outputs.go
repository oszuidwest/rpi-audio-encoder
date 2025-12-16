package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"maps"
	"math"
	"os/exec"
	"slices"
	"syscall"
	"time"
)

// startEnabledOutputs starts the audio distributor and all output processes.
func (m *Encoder) startEnabledOutputs() {
	go m.runDistributor()

	// Start all outputs
	for _, output := range m.config.GetOutputs() {
		if err := m.StartOutput(output.ID); err != nil {
			log.Printf("Failed to start output %s: %v", output.ID, err)
		}
	}
}

// Audio metering constants.
const (
	// Update levels every ~250ms (12000 stereo samples at 48kHz)
	levelUpdateSamples = 12000
	// Minimum dB level (silence)
	minDB = -60.0
	// Peak hold duration before decay
	peakHoldDuration = 1500 * time.Millisecond
)

// runDistributor delivers audio from the source to all output processes and calculates audio levels.
func (m *Encoder) runDistributor() {
	buf := make([]byte, 19200) // ~100ms of audio at 48kHz stereo

	// Level metering state
	var sampleCount int
	var sumSquaresL, sumSquaresR float64
	var peakL, peakR float64

	// Peak hold state
	heldPeakL, heldPeakR := minDB, minDB
	var peakHoldTimeL, peakHoldTimeR time.Time

	for {
		m.mu.RLock()
		state := m.state
		reader := m.sourceStdout
		stopChan := m.stopChan
		m.mu.RUnlock()

		if state != StateRunning || reader == nil {
			return
		}

		select {
		case <-stopChan:
			return
		default:
		}

		n, err := reader.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}

		// Calculate audio levels from S16LE stereo PCM
		// Format: [L_low, L_high, R_low, R_high, ...]
		for i := 0; i+3 < n; i += 4 {
			left := float64(int16(binary.LittleEndian.Uint16(buf[i:])))
			right := float64(int16(binary.LittleEndian.Uint16(buf[i+2:])))

			sumSquaresL += left * left
			sumSquaresR += right * right

			if absL := math.Abs(left); absL > peakL {
				peakL = absL
			}
			if absR := math.Abs(right); absR > peakR {
				peakR = absR
			}
			sampleCount++
		}

		// Update levels periodically
		if sampleCount >= levelUpdateSamples {
			rmsL := math.Sqrt(sumSquaresL / float64(sampleCount))
			rmsR := math.Sqrt(sumSquaresR / float64(sampleCount))

			// Convert to dB (reference: 32768 for 16-bit audio)
			dbL := 20 * math.Log10(rmsL/32768.0)
			dbR := 20 * math.Log10(rmsR/32768.0)
			peakDbL := 20 * math.Log10(peakL/32768.0)
			peakDbR := 20 * math.Log10(peakR/32768.0)

			// Clamp to minimum
			dbL = max(dbL, minDB)
			dbR = max(dbR, minDB)
			peakDbL = max(peakDbL, minDB)
			peakDbR = max(peakDbR, minDB)

			// Peak hold: update held peak if current is higher, or hold time expired
			now := time.Now()
			if peakDbL >= heldPeakL || now.Sub(peakHoldTimeL) > peakHoldDuration {
				heldPeakL = peakDbL
				peakHoldTimeL = now
			}
			if peakDbR >= heldPeakR || now.Sub(peakHoldTimeR) > peakHoldDuration {
				heldPeakR = peakDbR
				peakHoldTimeR = now
			}

			m.updateAudioLevels(dbL, dbR, heldPeakL, heldPeakR)

			// Reset accumulators
			sampleCount = 0
			sumSquaresL, sumSquaresR = 0, 0
			peakL, peakR = 0, 0
		}

		// Distribute to all running outputs
		m.mu.Lock()
		for _, proc := range m.outputProcesses {
			if proc.running && proc.stdin != nil {
				if _, err := proc.stdin.Write(buf[:n]); err != nil {
					log.Printf("Output write failed, marking as stopped: %v", err)
					proc.running = false
					if proc.stdin != nil {
						if closeErr := proc.stdin.Close(); closeErr != nil {
							log.Printf("Failed to close stdin: %v", closeErr)
						}
						proc.stdin = nil
					}
				}
			}
		}
		m.mu.Unlock()
	}
}

// stopAllOutputs stops all output processes.
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

// StartOutput starts an individual output FFmpeg process.
func (m *Encoder) StartOutput(outputID string) error {
	m.mu.Lock()
	if m.state != StateRunning {
		m.mu.Unlock()
		return fmt.Errorf("encoder not running")
	}

	existingProc, exists := m.outputProcesses[outputID]
	if exists && existingProc.running {
		m.mu.Unlock()
		return nil
	}

	var retryCount int
	retryDelay := initialRetryDelay
	if exists {
		retryCount = existingProc.retryCount
		retryDelay = existingProc.retryDelay
	}
	m.mu.Unlock()

	output := m.config.GetOutput(outputID)
	if output == nil {
		return fmt.Errorf("output not found: %s", outputID)
	}

	codecArgs := output.GetCodecArgs()
	format := output.GetOutputFormat()
	srtURL := fmt.Sprintf(
		"srt://%s:%d?pkt_size=1316&oheadbw=100&maxbw=-1&latency=10000000&mode=caller&transtype=live&streamid=%s&passphrase=%s",
		output.Host, output.Port, output.StreamID, output.Password,
	)

	args := []string{
		"-f", "s16le", "-ar", "48000", "-ac", "2",
		"-hide_banner", "-loglevel", "warning",
		"-i", "pipe:0", "-codec:a",
	}
	args = append(args, codecArgs...)
	args = append(args, "-f", format, srtURL)

	log.Printf("Starting output %s: %s:%d", outputID, output.Host, output.Port)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

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

	go m.runOutputProcess(outputID, cmd, &stderr)
	return nil
}

// runOutputProcess monitors the process and restarts on failure.
func (m *Encoder) runOutputProcess(outputID string, cmd *exec.Cmd, stderr *bytes.Buffer) {
	startTime := time.Now()
	err := cmd.Wait()
	runDuration := time.Since(startTime)

	m.mu.Lock()
	p, exists := m.outputProcesses[outputID]
	if !exists {
		m.mu.Unlock()
		return
	}

	p.running = false
	if err != nil {
		errMsg := extractLastError(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		p.lastError = errMsg
		log.Printf("Output %s error: %s", outputID, errMsg)

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

	if state != StateRunning {
		m.removeOutput(outputID)
		return
	}

	output := m.config.GetOutput(outputID)
	if output == nil {
		m.removeOutput(outputID)
		return
	}

	if retryCount >= maxRetries {
		log.Printf("Output %s failed %d times, giving up: %s", outputID, maxRetries, lastError)
		m.removeOutput(outputID)
		return
	}

	log.Printf("Output %s stopped, waiting %v before restart (attempt %d/%d)...",
		outputID, retryDelay, retryCount+1, maxRetries)
	time.Sleep(retryDelay)

	// Abort if output was removed or encoder stopped during wait
	output = m.config.GetOutput(outputID)
	if output == nil {
		log.Printf("Output %s was removed during retry wait, not restarting", outputID)
		m.removeOutput(outputID)
		return
	}

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

// StopOutput stops an output with graceful shutdown.
func (m *Encoder) StopOutput(outputID string) error {
	m.mu.Lock()
	proc, exists := m.outputProcesses[outputID]
	if !exists {
		m.mu.Unlock()
		return nil
	}

	if !proc.running {
		delete(m.outputProcesses, outputID)
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
		if cancel != nil {
			cancel()
		}
		m.removeOutput(outputID)
	}

	return nil
}

// removeOutput removes an output from the process map.
func (m *Encoder) removeOutput(outputID string) {
	m.mu.Lock()
	delete(m.outputProcesses, outputID)
	m.mu.Unlock()
}

// GetAllOutputStatuses returns status for all tracked outputs.
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
