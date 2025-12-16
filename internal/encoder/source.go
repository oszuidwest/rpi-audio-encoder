package encoder

import "runtime"

// GetSourceCommand returns the platform-specific command and arguments for audio capture.
func GetSourceCommand(input string) (string, []string) {
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
