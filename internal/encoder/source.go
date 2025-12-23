package encoder

import "runtime"

// GetSourceCommand returns the platform-specific command and arguments for audio capture.
func GetSourceCommand(input string) (cmd string, args []string) {
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
		// arecord provides ALSA capture with automatic sample rate conversion.
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
