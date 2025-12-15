package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Encoder handles FFmpeg process lifecycle and manages the source
// audio capture process along with multiple output encoding processes.
// Encoder is safe for concurrent use.
type Encoder struct {
	config           *Config
	sourceCmd        *exec.Cmd
	sourceCancel     context.CancelFunc
	sourceStdout     io.ReadCloser // Audio data from source FFmpeg
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

// NewEncoder creates a new FFmpeg manager
func NewEncoder(config *Config) *Encoder {
	return &Encoder{
		config:           config,
		state:            StateStopped,
		outputProcesses:  make(map[string]*OutputProcess),
		sourceRetryDelay: initialRetryDelay,
	}
}

// getAudioInputArgs returns FFmpeg arguments for audio input based on platform
// Returns input format options, then -i with the device
func (m *Encoder) getAudioInputArgs() []string {
	input := m.config.GetAudioInput()
	switch runtime.GOOS {
	case "darwin":
		if input == "" {
			input = ":0"
		}
		// macOS: avfoundation uses device's native settings
		return []string{"-f", "avfoundation", "-i", input}
	default: // linux
		if input == "" {
			input = "default:CARD=sndrpihifiberry"
		}
		// Linux: ALSA with sample rate and channels before -i
		return []string{
			"-f", "alsa",
			"-sample_rate", "48000",
			"-channels", "2",
			"-i", input,
		}
	}
}

// GetState returns the current encoder state
func (m *Encoder) GetState() EncoderState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// GetAudioLevels returns the current audio levels
func (m *Encoder) GetAudioLevels() AudioLevels {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state != StateRunning {
		return AudioLevels{Left: -60, Right: -60, PeakLeft: -60, PeakRight: -60}
	}
	return m.audioLevels
}

// GetStatus returns the current encoder status
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

// Start begins the source FFmpeg process and all enabled output processes
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

// Stop stops all FFmpeg processes with graceful shutdown
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
		log.Printf("Source FFmpeg stopped gracefully")
	case <-time.After(shutdownTimeout):
		log.Printf("Source FFmpeg did not stop in time, forcing kill")
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

// Restart stops and starts the encoder
func (m *Encoder) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	time.Sleep(1 * time.Second)
	return m.Start()
}

// runSourceLoop runs the source FFmpeg process with auto-restart
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
			log.Printf("Source FFmpeg error: %s", errMsg)

			if runDuration >= successThreshold {
				m.sourceRetryCount = 0
				m.sourceRetryDelay = initialRetryDelay
			} else {
				m.sourceRetryCount++
			}

			if m.sourceRetryCount >= maxRetries {
				log.Printf("Source FFmpeg failed %d times, giving up", maxRetries)
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

// runSource executes the source FFmpeg process
func (m *Encoder) runSource() (string, error) {
	// Audio filter for level metering: astats outputs to metadata, ametadata prints to stderr
	// reset=10 updates every ~200ms at 48kHz (reduces CPU overhead vs reset=1 which updates every frame)
	audioFilter := "astats=metadata=1:reset=10,ametadata=mode=print:file=/dev/stderr"

	// Build args: audio input (platform-specific) + output to stdout
	args := slices.Concat(m.getAudioInputArgs(), []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-af", audioFilter,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ac", "2",
		"-ar", "48000",
		"pipe:1", // Output to stdout
	})

	log.Printf("Starting source FFmpeg: %s -> stdout", m.config.GetAudioInput())

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	// Capture stdout for audio data
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", err
	}

	// Use pipe for stderr to stream and parse audio levels
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", err
	}

	// Buffer to capture error messages
	var stderrBuf bytes.Buffer

	m.mu.Lock()
	m.sourceCmd = cmd
	m.sourceCancel = cancel
	m.sourceStdout = stdoutPipe
	m.state = StateRunning
	m.startTime = time.Now()
	m.lastError = ""
	m.audioLevels = AudioLevels{Left: -60, Right: -60, PeakLeft: -60, PeakRight: -60}
	m.mu.Unlock()

	// Start the command
	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Parse stderr in a goroutine
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			// Try to parse audio levels from ametadata output
			if strings.HasPrefix(line, "lavfi.astats.") {
				m.parseAudioLevel(line)
			} else {
				// Store non-level lines for error reporting
				stderrBuf.WriteString(line + "\n")
			}
		}
	}()

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

	stderrOutput := extractLastError(stderrBuf.String())
	return stderrOutput, err
}

// parseAudioLevel parses a single audio level line from ametadata
func (m *Encoder) parseAudioLevel(line string) {
	// Expected format: lavfi.astats.1.RMS_level=-20.123 or lavfi.astats.1.Peak_level=-3.2
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return
	}

	value, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return
	}

	// Clamp to reasonable range
	if value < -60 {
		value = -60
	}
	if value > 0 {
		value = 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := parts[0]
	switch {
	case strings.Contains(key, ".1.RMS_level"):
		m.audioLevels.Left = value
		// For mono audio, copy to right channel as well
		m.audioLevels.Right = value
	case strings.Contains(key, ".2.RMS_level"):
		m.audioLevels.Right = value
	case strings.Contains(key, ".1.Peak_level"):
		m.audioLevels.PeakLeft = value
		// For mono audio, copy to right channel as well
		m.audioLevels.PeakRight = value
	case strings.Contains(key, ".2.Peak_level"):
		m.audioLevels.PeakRight = value
	}
}
