package recording

import (
	"github.com/oszuidwest/zwfm-encoder/internal/ffmpeg"
	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// BuildFFmpegArgs returns FFmpeg arguments for recording to a file.
func BuildFFmpegArgs(outputPath, codec string) []string {
	codecArgs := types.GetCodecArgs(codec)
	format := types.GetOutputFormat(codec)
	return ffmpeg.BuildArgsWithOverwrite(codecArgs, format, outputPath)
}
