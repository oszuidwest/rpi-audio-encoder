package audio

import (
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// SilenceConfig holds the configurable thresholds for silence detection.
type SilenceConfig struct {
	Threshold float64 // dB level below which audio is considered silent
	Duration  float64 // seconds of silence before alerting
	Recovery  float64 // seconds of audio before considering recovered
}

// SilenceDetector tracks silence state with hysteresis.
// It only reports "in silence" after Duration seconds of continuous silence,
// and only reports "recovered" after Recovery seconds of continuous audio.
type SilenceDetector struct {
	silenceStart  time.Time // when current silence period started
	recoveryStart time.Time // when audio returned after silence
	inSilence     bool      // currently in confirmed silence state
	webhookSent   bool
	emailSent     bool
}

// NewSilenceDetector creates a new silence detector.
func NewSilenceDetector() *SilenceDetector {
	return &SilenceDetector{}
}

// SilenceState contains the result of silence detection.
type SilenceState struct {
	IsSilent       bool               // True if in confirmed silence state
	Duration       float64            // How long in current silence (0 if not silent)
	Level          types.SilenceLevel // "active" when in silence, "" otherwise
	TriggerWebhook bool               // True when entering silence state for first time
	TriggerEmail   bool               // True when entering silence state for first time
}

// Update checks audio levels and returns the current silence state.
// Uses hysteresis: only enters silence after Duration seconds of quiet,
// only exits silence after Recovery seconds of audio.
func (d *SilenceDetector) Update(dbL, dbR float64, cfg SilenceConfig, now time.Time) SilenceState {
	audioIsSilent := dbL < cfg.Threshold && dbR < cfg.Threshold

	state := SilenceState{}

	if audioIsSilent {
		// Audio is currently below threshold
		d.recoveryStart = time.Time{} // reset recovery timer

		if d.silenceStart.IsZero() {
			d.silenceStart = now
		}

		silenceDuration := now.Sub(d.silenceStart).Seconds()

		if d.inSilence {
			// Already in confirmed silence state
			state.IsSilent = true
			state.Duration = silenceDuration
			state.Level = types.SilenceLevelActive
		} else if silenceDuration >= cfg.Duration {
			// Just crossed the duration threshold - enter silence state
			d.inSilence = true
			state.IsSilent = true
			state.Duration = silenceDuration
			state.Level = types.SilenceLevelActive

			// Trigger notifications once when entering silence
			if !d.webhookSent {
				d.webhookSent = true
				state.TriggerWebhook = true
			}
			if !d.emailSent {
				d.emailSent = true
				state.TriggerEmail = true
			}
		}
		// If not yet at duration threshold, state remains default (not in silence)
	} else {
		// Audio is above threshold
		d.silenceStart = time.Time{} // reset silence timer

		if d.inSilence {
			// Was in silence, now have audio - check recovery
			if d.recoveryStart.IsZero() {
				d.recoveryStart = now
			}

			recoveryDuration := now.Sub(d.recoveryStart).Seconds()

			if recoveryDuration >= cfg.Recovery {
				// Recovery complete - exit silence state
				d.inSilence = false
				d.webhookSent = false
				d.emailSent = false
				d.recoveryStart = time.Time{}
			} else {
				// Still in recovery period - remain in silence state
				state.IsSilent = true
				state.Level = types.SilenceLevelActive
			}
		}
	}

	return state
}

// Reset clears the silence detection state.
func (d *SilenceDetector) Reset() {
	d.silenceStart = time.Time{}
	d.recoveryStart = time.Time{}
	d.inSilence = false
	d.webhookSent = false
	d.emailSent = false
}
