package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Encoder manages audio capture and multiple output encoding processes.
// It is safe for concurrent use.
type Encoder struct {
	config           *Config
	sourceCmd        *exec.Cmd
	sourceCancel     context.CancelFunc
	sourceStdout     io.ReadCloser // Raw PCM audio data from source
	outputProcesses  map[string]*OutputProcess
	state            EncoderState
	stopChan         chan struct{}
	mu               sync.RWMutex
	lastError        string
	startTime        time.Time
	sourceRetryCount int
	sourceRetryDelay time.Duration
	audioLevels      AudioLevels
}

// NewEncoder creates a new Encoder with the given configuration.
func NewEncoder(config *Config) *Encoder {
	return &Encoder{
		config:           config,
		state:            StateStopped,
		outputProcesses:  make(map[string]*OutputProcess),
		sourceRetryDelay: initialRetryDelay,
	}
}

// getSourceCommand returns the command and arguments for audio capture.
// Linux uses arecord for minimal CPU overhead.
// macOS uses FFmpeg with AVFoundation (for development).
func (m *Encoder) getSourceCommand() (string, []string) {
	input := m.config.GetAudioInput()

	switch runtime.GOOS {
	case "darwin":
		if input == "" {
			input = ":0"
		}
		return "ffmpeg", []string{
			"-f", "avfoundation",
			"-i", input,
			"-nostdin",
			"-hide_banner",
			"-loglevel", "warning",
			"-vn",
			"-f", "s16le",
			"-ac", "2",
			"-ar", "48000",
			"pipe:1",
		}
	default: // linux
		if input == "" {
			input = "default:CARD=sndrpihifiberry"
		}
		// arecord: minimal ALSA capture tool, much lower overhead than FFmpeg
		// ALSA plug layer handles sample rate conversion if source differs from 48kHz
		return "arecord", []string{
			"-D", input,
			"-f", "S16_LE",
			"-r", "48000",
			"-c", "2",
			"-t", "raw",
			"-q",
			"-",
		}
	}
}

// GetState returns the current encoder state.
func (m *Encoder) GetState() EncoderState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// GetAudioLevels returns the current audio levels.
func (m *Encoder) GetAudioLevels() AudioLevels {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state != StateRunning {
		return AudioLevels{Left: -60, Right: -60, PeakLeft: -60, PeakRight: -60}
	}
	return m.audioLevels
}

// GetStatus returns the current encoder status.
func (m *Encoder) GetStatus() EncoderStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	uptime := ""
	if m.state == StateRunning {
		d := time.Since(m.startTime)
		uptime = fmt.Sprintf("%dh %dm %ds", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}

	runningOutputs := 0
	for _, proc := range m.outputProcesses {
		if proc.running {
			runningOutputs++
		}
	}

	return EncoderStatus{
		State:            m.state,
		Uptime:           uptime,
		LastError:        m.lastError,
		OutputCount:      runningOutputs,
		SourceRetryCount: m.sourceRetryCount,
		SourceMaxRetries: maxRetries,
	}
}

// Start begins audio capture and all output processes.
func (m *Encoder) Start() error {
	m.mu.Lock()

	if m.state == StateRunning || m.state == StateStarting {
		m.mu.Unlock()
		return fmt.Errorf("encoder already running")
	}

	m.state = StateStarting
	m.stopChan = make(chan struct{})
	m.sourceRetryCount = 0
	m.sourceRetryDelay = initialRetryDelay
	m.mu.Unlock()

	go m.runSourceLoop()

	return nil
}

// Stop stops all processes with graceful shutdown.
func (m *Encoder) Stop() error {
	m.mu.Lock()

	if m.state == StateStopped || m.state == StateStopping {
		m.mu.Unlock()
		return nil
	}

	m.state = StateStopping

	if m.stopChan != nil {
		close(m.stopChan)
	}

	// Get references while holding lock
	sourceProcess := m.sourceCmd
	sourceCancel := m.sourceCancel
	m.mu.Unlock()

	// Stop all outputs first
	m.stopAllOutputs()

	// Send SIGINT to source for graceful shutdown
	if sourceProcess != nil && sourceProcess.Process != nil {
		if err := sourceProcess.Process.Signal(syscall.SIGINT); err != nil {
			// Process might already be dead
			log.Printf("Failed to send SIGINT to source: %v", err)
		}
	}

	// Wait for source to stop with timeout
	// The runSourceLoop goroutine handles cmd.Wait()
	stopped := pollUntil(func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.sourceCmd == nil
	})

	select {
	case <-stopped:
		log.Printf("Source capture stopped gracefully")
	case <-time.After(shutdownTimeout):
		log.Printf("Source capture did not stop in time, forcing kill")
		// Force kill via context cancellation
		if sourceCancel != nil {
			sourceCancel()
		}
	}

	m.mu.Lock()
	m.state = StateStopped
	m.sourceCmd = nil
	m.sourceCancel = nil
	m.mu.Unlock()

	return nil
}

// Restart stops and starts the encoder.
func (m *Encoder) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	time.Sleep(1 * time.Second)
	return m.Start()
}

// runSourceLoop runs the audio capture process with auto-restart.
func (m *Encoder) runSourceLoop() {
	for {
		m.mu.Lock()
		if m.state == StateStopping || m.state == StateStopped {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		startTime := time.Now()
		stderrOutput, err := m.runSource()
		runDuration := time.Since(startTime)

		m.mu.Lock()
		if err != nil {
			errMsg := err.Error()
			if stderrOutput != "" {
				errMsg = stderrOutput
			}
			m.lastError = errMsg
			log.Printf("Source capture error: %s", errMsg)

			if runDuration >= successThreshold {
				m.sourceRetryCount = 0
				m.sourceRetryDelay = initialRetryDelay
			} else {
				m.sourceRetryCount++
			}

			if m.sourceRetryCount >= maxRetries {
				log.Printf("Source capture failed %d times, giving up", maxRetries)
				m.state = StateStopped
				m.lastError = fmt.Sprintf("Stopped after %d failed attempts: %s", maxRetries, errMsg)
				m.mu.Unlock()
				m.stopAllOutputs()
				return
			}
		} else {
			m.sourceRetryCount = 0
			m.sourceRetryDelay = initialRetryDelay
		}

		if m.state == StateStopping || m.state == StateStopped {
			m.mu.Unlock()
			return
		}

		m.state = StateStarting
		retryDelay := m.sourceRetryDelay
		m.mu.Unlock()

		log.Printf("Source stopped, waiting %v before restart (attempt %d/%d)...",
			retryDelay, m.sourceRetryCount+1, maxRetries)
		select {
		case <-m.stopChan:
			return
		case <-time.After(retryDelay):
			m.mu.Lock()
			m.sourceRetryDelay *= 2
			if m.sourceRetryDelay > maxRetryDelay {
				m.sourceRetryDelay = maxRetryDelay
			}
			m.mu.Unlock()
		}
	}
}

// runSource executes the audio capture process.
func (m *Encoder) runSource() (string, error) {
	cmdName, args := m.getSourceCommand()

	log.Printf("Starting audio capture: %s %s", cmdName, m.config.GetAudioInput())

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, cmdName, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", err
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	m.mu.Lock()
	m.sourceCmd = cmd
	m.sourceCancel = cancel
	m.sourceStdout = stdoutPipe
	m.state = StateRunning
	m.startTime = time.Now()
	m.lastError = ""
	m.audioLevels = AudioLevels{Left: -60, Right: -60, PeakLeft: -60, PeakRight: -60}
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Start distributor and outputs after brief delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		m.startEnabledOutputs()
	}()

	err = cmd.Wait()

	m.mu.Lock()
	m.sourceCmd = nil
	m.sourceCancel = nil
	m.sourceStdout = nil
	m.mu.Unlock()

	return extractLastError(stderrBuf.String()), err
}

// updateAudioLevels updates audio levels from calculated RMS and peak values.
func (m *Encoder) updateAudioLevels(rmsL, rmsR, peakL, peakR float64) {
	m.mu.Lock()
	m.audioLevels = AudioLevels{
		Left:      rmsL,
		Right:     rmsR,
		PeakLeft:  peakL,
		PeakRight: peakR,
	}
	m.mu.Unlock()
}
