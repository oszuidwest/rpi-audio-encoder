// Package ffmpeg provides shared FFmpeg utilities and constants.
package ffmpeg

import "bytes"

// MaxStderrSize limits the stderr buffer to prevent memory exhaustion.
const MaxStderrSize = 64 * 1024 // 64KB

// ExtractLastError extracts the last meaningful error line from FFmpeg stderr.
// Returns empty string if no meaningful error found.
func ExtractLastError(stderr string) string {
	if stderr == "" {
		return ""
	}
	lines := bytes.Split([]byte(stderr), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := string(bytes.TrimSpace(lines[i]))
		if line != "" {
			if len(line) > 200 {
				return line[:200] + "..."
			}
			return line
		}
	}
	return ""
}

// BaseInputArgs returns the common FFmpeg arguments for PCM input from stdin.
// These args configure FFmpeg to read raw S16LE stereo audio at 48kHz.
func BaseInputArgs() []string {
	return []string{
		"-f", "s16le",
		"-ar", "48000",
		"-ac", "2",
		"-hide_banner",
		"-loglevel", "warning",
		"-i", "pipe:0",
	}
}

// BuildArgs constructs complete FFmpeg arguments by combining base input args
// with codec args, format, and output destination.
func BuildArgs(codecArgs []string, format, output string) []string {
	args := BaseInputArgs()
	args = append(args, "-codec:a")
	args = append(args, codecArgs...)
	args = append(args, "-f", format)
	args = append(args, output)
	return args
}

// BuildArgsWithOverwrite is like BuildArgs but adds -y flag for file overwriting.
func BuildArgsWithOverwrite(codecArgs []string, format, output string) []string {
	args := BaseInputArgs()
	args = append(args, "-codec:a")
	args = append(args, codecArgs...)
	args = append(args, "-f", format, "-y", output)
	return args
}
