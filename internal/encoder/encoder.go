package encoder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/audio"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/notify"
	"github.com/oszuidwest/zwfm-encoder/internal/output"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// LevelUpdateSamples is the number of samples before updating audio levels (~250ms).
const LevelUpdateSamples = 12000

// Encoder manages audio capture and multiple output encoding processes.
type Encoder struct {
	config        *config.Config
	outputManager *output.Manager
	sourceCmd     *exec.Cmd
	sourceCancel  context.CancelFunc
	sourceStdout  io.ReadCloser
	state         types.EncoderState
	stopChan      chan struct{}
	mu            sync.RWMutex
	lastError     string
	startTime     time.Time
	retryCount    int
	retryDelay    time.Duration
	audioLevels   types.AudioLevels
	silenceDetect *audio.SilenceDetector
	peakHolder    *audio.PeakHolder
}

// New creates a new Encoder with the given configuration.
func New(cfg *config.Config) *Encoder {
	return &Encoder{
		config:        cfg,
		outputManager: output.NewManager(),
		state:         types.StateStopped,
		retryDelay:    types.InitialRetryDelay,
		silenceDetect: audio.NewSilenceDetector(),
		peakHolder:    audio.NewPeakHolder(),
	}
}

// GetState returns the current encoder state.
func (e *Encoder) GetState() types.EncoderState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// GetAudioLevels returns the current audio levels.
func (e *Encoder) GetAudioLevels() types.AudioLevels {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state != types.StateRunning {
		return types.AudioLevels{Left: -60, Right: -60, PeakLeft: -60, PeakRight: -60}
	}
	return e.audioLevels
}

// GetStatus returns the current encoder status.
func (e *Encoder) GetStatus() types.EncoderStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	uptime := ""
	if e.state == types.StateRunning {
		d := time.Since(e.startTime)
		uptime = fmt.Sprintf("%dh %dm %ds", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}

	return types.EncoderStatus{
		State:            e.state,
		Uptime:           uptime,
		LastError:        e.lastError,
		SourceRetryCount: e.retryCount,
		SourceMaxRetries: types.MaxRetries,
	}
}

// GetAllOutputStatuses returns status for all tracked outputs.
func (e *Encoder) GetAllOutputStatuses() map[string]types.OutputStatus {
	return e.outputManager.GetAllStatuses(func(id string) int {
		if o := e.config.GetOutput(id); o != nil {
			return o.GetMaxRetries()
		}
		return types.DefaultMaxRetries
	})
}

// Start begins audio capture and all output processes.
func (e *Encoder) Start() error {
	e.mu.Lock()

	if e.state == types.StateRunning || e.state == types.StateStarting {
		e.mu.Unlock()
		return fmt.Errorf("encoder already running")
	}

	e.state = types.StateStarting
	e.stopChan = make(chan struct{})
	e.retryCount = 0
	e.retryDelay = types.InitialRetryDelay
	e.silenceDetect.Reset()
	e.peakHolder.Reset()
	e.mu.Unlock()

	go e.runSourceLoop()

	return nil
}

// Stop stops all processes with graceful shutdown.
func (e *Encoder) Stop() error {
	e.mu.Lock()

	if e.state == types.StateStopped || e.state == types.StateStopping {
		e.mu.Unlock()
		return nil
	}

	e.state = types.StateStopping

	if e.stopChan != nil {
		close(e.stopChan)
	}

	// Get references while holding lock
	sourceProcess := e.sourceCmd
	sourceCancel := e.sourceCancel
	e.mu.Unlock()

	// Stop all outputs first
	e.outputManager.StopAll()

	// Send SIGINT to source for graceful shutdown
	if sourceProcess != nil && sourceProcess.Process != nil {
		if err := sourceProcess.Process.Signal(syscall.SIGINT); err != nil {
			log.Printf("Failed to send SIGINT to source: %v", err)
		}
	}

	// Wait for source to stop with timeout
	stopped := e.pollUntil(func() bool {
		e.mu.RLock()
		defer e.mu.RUnlock()
		return e.sourceCmd == nil
	})

	select {
	case <-stopped:
		log.Printf("Source capture stopped gracefully")
	case <-time.After(types.ShutdownTimeout):
		log.Printf("Source capture did not stop in time, forcing kill")
		if sourceCancel != nil {
			sourceCancel()
		}
	}

	e.mu.Lock()
	e.state = types.StateStopped
	e.sourceCmd = nil
	e.sourceCancel = nil
	e.mu.Unlock()

	return nil
}

// Restart stops and starts the encoder.
func (e *Encoder) Restart() error {
	if err := e.Stop(); err != nil {
		return err
	}
	time.Sleep(1 * time.Second)
	return e.Start()
}

// StartOutput starts an individual output FFmpeg process.
func (e *Encoder) StartOutput(outputID string) error {
	e.mu.RLock()
	if e.state != types.StateRunning {
		e.mu.RUnlock()
		return fmt.Errorf("encoder not running")
	}
	e.mu.RUnlock()

	out := e.config.GetOutput(outputID)
	if out == nil {
		return fmt.Errorf("output not found: %s", outputID)
	}

	// Get existing retry state if any
	_, retryCount, retryDelay, exists := e.outputManager.GetProcess(outputID)
	if !exists {
		retryCount = 0
		retryDelay = types.InitialRetryDelay
	}

	if err := e.outputManager.Start(out, retryCount, retryDelay); err != nil {
		return fmt.Errorf("failed to start output: %w", err)
	}

	// Start monitoring goroutine
	go e.monitorOutput(outputID)

	return nil
}

// StopOutput stops an output by ID.
func (e *Encoder) StopOutput(outputID string) error {
	return e.outputManager.Stop(outputID)
}

// TriggerTestEmail sends a test email to verify configuration.
func (e *Encoder) TriggerTestEmail() error {
	cfg := notify.EmailConfig{
		Host:       e.config.GetEmailSMTPHost(),
		Port:       e.config.GetEmailSMTPPort(),
		Username:   e.config.GetEmailUsername(),
		Password:   e.config.GetEmailPassword(),
		Recipients: e.config.GetEmailRecipients(),
	}
	return notify.SendTestEmail(cfg)
}

// runSourceLoop runs the audio capture process with auto-restart.
func (e *Encoder) runSourceLoop() {
	for {
		e.mu.Lock()
		if e.state == types.StateStopping || e.state == types.StateStopped {
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()

		startTime := time.Now()
		stderrOutput, err := e.runSource()
		runDuration := time.Since(startTime)

		e.mu.Lock()
		if err != nil {
			errMsg := err.Error()
			if stderrOutput != "" {
				errMsg = stderrOutput
			}
			e.lastError = errMsg
			log.Printf("Source capture error: %s", errMsg)

			if runDuration >= types.SuccessThreshold {
				e.retryCount = 0
				e.retryDelay = types.InitialRetryDelay
			} else {
				e.retryCount++
			}

			if e.retryCount >= types.MaxRetries {
				log.Printf("Source capture failed %d times, giving up", types.MaxRetries)
				e.state = types.StateStopped
				e.lastError = fmt.Sprintf("Stopped after %d failed attempts: %s", types.MaxRetries, errMsg)
				e.mu.Unlock()
				e.outputManager.StopAll()
				return
			}
		} else {
			e.retryCount = 0
			e.retryDelay = types.InitialRetryDelay
		}

		if e.state == types.StateStopping || e.state == types.StateStopped {
			e.mu.Unlock()
			return
		}

		e.state = types.StateStarting
		retryDelay := e.retryDelay
		e.mu.Unlock()

		log.Printf("Source stopped, waiting %v before restart (attempt %d/%d)...",
			retryDelay, e.retryCount+1, types.MaxRetries)
		select {
		case <-e.stopChan:
			return
		case <-time.After(retryDelay):
			e.mu.Lock()
			e.retryDelay *= 2
			if e.retryDelay > types.MaxRetryDelay {
				e.retryDelay = types.MaxRetryDelay
			}
			e.mu.Unlock()
		}
	}
}

// runSource executes the audio capture process.
func (e *Encoder) runSource() (string, error) {
	cmdName, args := GetSourceCommand(e.config.GetAudioInput())

	log.Printf("Starting audio capture: %s %s", cmdName, e.config.GetAudioInput())

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, cmdName, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", err
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	e.mu.Lock()
	e.sourceCmd = cmd
	e.sourceCancel = cancel
	e.sourceStdout = stdoutPipe
	e.state = types.StateRunning
	e.startTime = time.Now()
	e.lastError = ""
	e.audioLevels = types.AudioLevels{Left: -60, Right: -60, PeakLeft: -60, PeakRight: -60}
	e.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Start distributor and outputs after brief delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		e.startEnabledOutputs()
	}()

	err = cmd.Wait()

	e.mu.Lock()
	e.sourceCmd = nil
	e.sourceCancel = nil
	e.sourceStdout = nil
	e.mu.Unlock()

	return extractLastError(stderrBuf.String()), err
}

// startEnabledOutputs starts the audio distributor and all output processes.
func (e *Encoder) startEnabledOutputs() {
	go e.runDistributor()

	for _, out := range e.config.GetOutputs() {
		if err := e.StartOutput(out.ID); err != nil {
			log.Printf("Failed to start output %s: %v", out.ID, err)
		}
	}
}

// runDistributor delivers audio from the source to all output processes and calculates audio levels.
func (e *Encoder) runDistributor() {
	buf := make([]byte, 19200) // ~100ms of audio at 48kHz stereo
	levelData := &audio.LevelData{}

	for {
		e.mu.RLock()
		state := e.state
		reader := e.sourceStdout
		stopChan := e.stopChan
		e.mu.RUnlock()

		if state != types.StateRunning || reader == nil {
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

		// Process samples for level metering
		audio.ProcessSamples(buf, n, levelData)

		// Update levels periodically
		if levelData.SampleCount >= LevelUpdateSamples {
			levels := audio.CalculateLevels(levelData)

			// Update peak hold
			now := time.Now()
			heldPeakL, heldPeakR := e.peakHolder.Update(levels.PeakL, levels.PeakR, now)

			// Silence detection
			silenceCfg := audio.SilenceConfig{
				Threshold: e.config.GetSilenceThreshold(),
				Duration:  e.config.GetSilenceDuration(),
				Recovery:  e.config.GetSilenceRecovery(),
			}
			silenceState := e.silenceDetect.Update(levels.RMSL, levels.RMSR, silenceCfg, now)

			// Trigger notifications if needed
			if silenceState.TriggerWebhook {
				go e.triggerSilenceWebhook(silenceState.Duration)
			}
			if silenceState.TriggerEmail {
				go e.triggerSilenceEmail(silenceState.Duration)
			}

			e.updateAudioLevels(levels.RMSL, levels.RMSR, heldPeakL, heldPeakR,
				silenceState.IsSilent, silenceState.Duration, silenceState.Level,
				levels.ClipL, levels.ClipR)

			audio.ResetLevelData(levelData)
		}

		// Distribute to all running outputs
		for _, out := range e.config.GetOutputs() {
			// WriteAudio logs errors internally and marks output as stopped
			_ = e.outputManager.WriteAudio(out.ID, buf[:n]) //nolint:errcheck
		}
	}
}

// monitorOutput monitors an output process and restarts on failure.
func (e *Encoder) monitorOutput(outputID string) {
	cmd, retryCount, retryDelay, exists := e.outputManager.GetProcess(outputID)
	if !exists || cmd == nil {
		return
	}

	startTime := time.Now()
	err := cmd.Wait()
	runDuration := time.Since(startTime)

	e.outputManager.MarkStopped(outputID)

	if err != nil {
		// Extract error and update state
		var errMsg string
		if stderr, ok := cmd.Stderr.(*bytes.Buffer); ok && stderr != nil {
			errMsg = extractLastError(stderr.String())
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		e.outputManager.SetError(outputID, errMsg)
		log.Printf("Output %s error: %s", outputID, errMsg)

		if runDuration >= types.SuccessThreshold {
			retryCount = 0
			retryDelay = types.InitialRetryDelay
		} else {
			retryCount++
			retryDelay *= 2
			if retryDelay > types.MaxRetryDelay {
				retryDelay = types.MaxRetryDelay
			}
		}
		e.outputManager.UpdateRetry(outputID, retryCount, retryDelay)
	} else {
		e.outputManager.UpdateRetry(outputID, 0, types.InitialRetryDelay)
	}

	// Check if we should retry
	e.mu.RLock()
	state := e.state
	e.mu.RUnlock()

	if state != types.StateRunning {
		e.outputManager.Remove(outputID)
		return
	}

	out := e.config.GetOutput(outputID)
	if out == nil {
		e.outputManager.Remove(outputID)
		return
	}

	maxRetries := out.GetMaxRetries()
	if retryCount > maxRetries {
		log.Printf("Output %s gave up after %d retries", outputID, maxRetries)
		return // Keep in outputProcesses for status reporting
	}

	log.Printf("Output %s stopped, waiting %v before retry %d/%d...",
		outputID, retryDelay, retryCount, maxRetries)
	time.Sleep(retryDelay)

	// Abort if output was removed or encoder stopped during wait
	out = e.config.GetOutput(outputID)
	if out == nil {
		log.Printf("Output %s was removed during retry wait, not restarting", outputID)
		e.outputManager.Remove(outputID)
		return
	}

	e.mu.RLock()
	state = e.state
	e.mu.RUnlock()
	if state != types.StateRunning {
		e.outputManager.Remove(outputID)
		return
	}

	if err := e.StartOutput(outputID); err != nil {
		log.Printf("Failed to restart output %s: %v", outputID, err)
		e.outputManager.Remove(outputID)
	}
}

// updateAudioLevels updates audio levels from calculated values.
func (e *Encoder) updateAudioLevels(rmsL, rmsR, peakL, peakR float64, silence bool, silenceDuration float64, silenceLevel types.SilenceLevel, clipL, clipR int) {
	e.mu.Lock()
	e.audioLevels = types.AudioLevels{
		Left:            rmsL,
		Right:           rmsR,
		PeakLeft:        peakL,
		PeakRight:       peakR,
		Silence:         silence,
		SilenceDuration: silenceDuration,
		SilenceLevel:    silenceLevel,
		ClipLeft:        clipL,
		ClipRight:       clipR,
	}
	e.mu.Unlock()
}

// triggerSilenceWebhook sends a webhook notification for critical silence.
func (e *Encoder) triggerSilenceWebhook(duration float64) {
	webhookURL := e.config.GetSilenceWebhook()
	threshold := e.config.GetSilenceThreshold()
	if err := notify.SendSilenceWebhook(webhookURL, duration, threshold); err != nil {
		log.Printf("Silence webhook failed: %v", err)
	} else if webhookURL != "" {
		log.Printf("Silence webhook sent successfully (duration: %.1fs)", duration)
	}
}

// triggerSilenceEmail sends an email notification for critical silence.
func (e *Encoder) triggerSilenceEmail(duration float64) {
	cfg := notify.EmailConfig{
		Host:       e.config.GetEmailSMTPHost(),
		Port:       e.config.GetEmailSMTPPort(),
		Username:   e.config.GetEmailUsername(),
		Password:   e.config.GetEmailPassword(),
		Recipients: e.config.GetEmailRecipients(),
	}
	threshold := e.config.GetSilenceThreshold()
	if err := notify.SendSilenceAlert(cfg, duration, threshold); err != nil {
		log.Printf("Silence email failed: %v", err)
	} else if cfg.Host != "" && cfg.Recipients != "" {
		log.Printf("Silence email sent successfully (duration: %.1fs)", duration)
	}
}

// pollUntil signals when the given condition becomes true.
func (e *Encoder) pollUntil(condition func() bool) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for !condition() {
			time.Sleep(types.PollInterval)
		}
		close(done)
	}()
	return done
}
