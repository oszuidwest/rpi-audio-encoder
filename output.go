package main

// Output represents a single SRT output destination.
type Output struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Password   string `json:"password"`
	StreamID   string `json:"streamid"`
	Codec      string `json:"codec"`
	MaxRetries int    `json:"max_retries,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// DefaultMaxRetries is the default number of retry attempts for outputs.
const DefaultMaxRetries = 99

// GetMaxRetries returns the configured max retries or the default value.
func (o *Output) GetMaxRetries() int {
	if o.MaxRetries <= 0 {
		return DefaultMaxRetries
	}
	return o.MaxRetries
}

// codecPreset defines FFmpeg encoding parameters for a codec.
type codecPreset struct {
	args   []string
	format string
}

// codecPresets maps codec names to their FFmpeg configuration
var codecPresets = map[string]codecPreset{
	"mp2": {[]string{"libtwolame", "-b:a", "384k", "-psymodel", "4"}, "mp2"},
	"mp3": {[]string{"libmp3lame", "-b:a", "320k"}, "mp3"},
	"ogg": {[]string{"libvorbis", "-qscale:a", "10"}, "ogg"},
	"wav": {[]string{"pcm_s16le"}, "matroska"},
}

// defaultCodec is used when an unknown codec is specified
const defaultCodec = "mp3"

// GetCodecArgs returns FFmpeg codec arguments for this output's codec.
func (o *Output) GetCodecArgs() []string {
	if preset, ok := codecPresets[o.Codec]; ok {
		return preset.args
	}
	return codecPresets[defaultCodec].args
}

// GetOutputFormat returns the FFmpeg output format for this output's codec.
func (o *Output) GetOutputFormat() string {
	if preset, ok := codecPresets[o.Codec]; ok {
		return preset.format
	}
	return codecPresets[defaultCodec].format
}
