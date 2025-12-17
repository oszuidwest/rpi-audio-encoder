package encoder

import (
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/audio"
	"github.com/oszuidwest/zwfm-encoder/internal/notify"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// AudioLevelCallback is called when audio levels are updated.
type AudioLevelCallback func(rmsL, rmsR, peakL, peakR float64, silence bool, silenceDuration float64, silenceLevel types.SilenceLevel, clipL, clipR int)

// Distributor handles audio sample processing, level metering, and silence detection.
// It encapsulates the audio processing pipeline separate from the distribution logic.
type Distributor struct {
	levelData       *audio.LevelData
	silenceDetect   *audio.SilenceDetector
	silenceNotifier *notify.SilenceNotifier
	peakHolder      *audio.PeakHolder
	silenceCfg      audio.SilenceConfig
	callback        AudioLevelCallback
}

// NewDistributor creates a new audio distributor with the given configuration and callback.
// The silence config is snapshotted at creation to avoid mutex contention in the hot path.
func NewDistributor(silenceDetect *audio.SilenceDetector, silenceNotifier *notify.SilenceNotifier, peakHolder *audio.PeakHolder, silenceCfg audio.SilenceConfig, callback AudioLevelCallback) *Distributor {
	return &Distributor{
		levelData:       &audio.LevelData{},
		silenceDetect:   silenceDetect,
		silenceNotifier: silenceNotifier,
		peakHolder:      peakHolder,
		silenceCfg:      silenceCfg,
		callback:        callback,
	}
}

// ProcessSamples processes a buffer of audio samples for level metering and silence detection.
// It accumulates sample data and periodically calculates levels, updates peak hold,
// runs silence detection, and triggers notifications.
func (d *Distributor) ProcessSamples(buf []byte, n int) {
	// Process samples for level metering
	audio.ProcessSamples(buf, n, d.levelData)

	// Update levels periodically
	if d.levelData.SampleCount >= LevelUpdateSamples {
		levels := audio.CalculateLevels(d.levelData)

		// Update peak hold
		now := time.Now()
		heldPeakL, heldPeakR := d.peakHolder.Update(levels.PeakL, levels.PeakR, now)

		// Silence detection (using snapshot from startup)
		silenceEvent := d.silenceDetect.Update(levels.RMSL, levels.RMSR, d.silenceCfg, now)

		// Delegate notification handling to the notifier (separation of concerns)
		d.silenceNotifier.HandleEvent(silenceEvent)

		// Report levels via callback
		if d.callback != nil {
			d.callback(levels.RMSL, levels.RMSR, heldPeakL, heldPeakR,
				silenceEvent.InSilence, silenceEvent.Duration, silenceEvent.Level,
				levels.ClipL, levels.ClipR)
		}

		audio.ResetLevelData(d.levelData)
	}
}
