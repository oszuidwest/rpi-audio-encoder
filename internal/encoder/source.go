package encoder

import (
	"errors"
	"runtime"
)

// ErrNoAudioDevice is returned when no audio input device is available.
var ErrNoAudioDevice = errors.New("no audio input device found")

// GetSourceCommand returns the platform-specific command and arguments for audio capture.
// Returns ErrNoAudioDevice if no device is configured or detected.
func GetSourceCommand(input string) (string, []string, error) {
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
		}, nil
	case "windows":
		if input == "" {
			// Auto-detect first available audio device.
			if devices := ListAudioDevices(); len(devices) > 0 {
				input = devices[0].ID
			} else {
				return "", nil, ErrNoAudioDevice
			}
		}
		return "ffmpeg", []string{
			"-f", "dshow",
			"-i", input,
			"-nostdin",
			"-hide_banner",
			"-loglevel", "warning",
			"-vn",
			"-f", "s16le",
			"-ac", "2",
			"-ar", "48000",
			"pipe:1",
		}, nil
	default: // linux
		if input == "" {
			input = "default:CARD=sndrpihifiberry"
		}
		// arecord provides ALSA capture with automatic sample rate conversion.
		return "arecord", []string{
			"-D", input,
			"-f", "S16_LE",
			"-r", "48000",
			"-c", "2",
			"-t", "raw",
			"-q",
			"-",
		}, nil
	}
}
