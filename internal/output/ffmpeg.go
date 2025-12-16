// Package output manages FFmpeg output processes for streaming.
package output

import (
	"fmt"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// BuildFFmpegArgs returns the FFmpeg arguments for an output.
func BuildFFmpegArgs(output *types.Output) []string {
	codecArgs := output.GetCodecArgs()
	format := output.GetOutputFormat()
	srtURL := BuildSRTURL(output)

	args := []string{
		"-f", "s16le", "-ar", "48000", "-ac", "2",
		"-hide_banner", "-loglevel", "warning",
		"-i", "pipe:0", "-codec:a",
	}
	args = append(args, codecArgs...)
	args = append(args, "-f", format, srtURL)
	return args
}

// BuildSRTURL constructs the SRT URL for an output.
func BuildSRTURL(output *types.Output) string {
	return fmt.Sprintf(
		"srt://%s:%d?pkt_size=1316&oheadbw=100&maxbw=-1&latency=10000000&mode=caller&transtype=live&streamid=%s&passphrase=%s",
		output.Host, output.Port, output.StreamID, output.Password,
	)
}
