// Package config provides application configuration management.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// Default values.
const (
	DefaultSilenceThreshold = -40.0
	DefaultSilenceDuration  = 15.0 // seconds of silence before alerting
	DefaultSilenceRecovery  = 5.0  // seconds of audio before considering recovered
	DefaultEmailSMTPPort    = 587
)

// Config holds all application configuration. It is safe for concurrent use.
type Config struct {
	WebPort          int            `json:"web_port"`
	WebUser          string         `json:"web_user"`
	WebPassword      string         `json:"web_password"`
	AudioInput       string         `json:"audio_input"`
	SilenceThreshold float64        `json:"silence_threshold,omitempty"`
	SilenceDuration  float64        `json:"silence_duration,omitempty"`
	SilenceRecovery  float64        `json:"silence_recovery,omitempty"`
	SilenceWebhook   string         `json:"silence_webhook,omitempty"`
	EmailSMTPHost    string         `json:"email_smtp_host,omitempty"`
	EmailSMTPPort    int            `json:"email_smtp_port,omitempty"`
	EmailUsername    string         `json:"email_username,omitempty"`
	EmailPassword    string         `json:"email_password,omitempty"`
	EmailRecipients  string         `json:"email_recipients,omitempty"`
	Outputs          []types.Output `json:"outputs"`

	mu       sync.RWMutex
	filePath string
}

// New creates a new Config with default values.
func New(filePath string) *Config {
	return &Config{
		WebPort:     8080,
		WebUser:     "admin",
		WebPassword: "encoder",
		AudioInput:  defaultAudioInput(),
		Outputs:     []types.Output{},
		filePath:    filePath,
	}
}

// defaultAudioInput returns the default audio input device for the current platform.
func defaultAudioInput() string {
	if runtime.GOOS == "darwin" {
		return ":0"
	}
	return "default:CARD=sndrpihifiberry"
}

// Load reads config from file, creating a default if none exists.
func (c *Config) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if os.IsNotExist(err) {
		return c.saveLocked()
	}
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Migrate old configs
	for i := range c.Outputs {
		if c.Outputs[i].Codec == "" {
			c.Outputs[i].Codec = "mp3"
		}
		if c.Outputs[i].CreatedAt == 0 {
			c.Outputs[i].CreatedAt = time.Now().UnixMilli()
		}
	}

	if c.AudioInput == "" {
		c.AudioInput = defaultAudioInput()
	}

	return nil
}

// Save writes the configuration to file.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveLocked()
}

// saveLocked writes config to file. Caller must hold c.mu.
func (c *Config) saveLocked() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(c.filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// GetOutputs returns a copy of all outputs.
func (c *Config) GetOutputs() []types.Output {
	c.mu.RLock()
	defer c.mu.RUnlock()

	outputs := make([]types.Output, len(c.Outputs))
	copy(outputs, c.Outputs)
	return outputs
}

// GetOutput returns a copy of the output with the given ID, or nil if not found.
func (c *Config) GetOutput(id string) *types.Output {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, o := range c.Outputs {
		if o.ID == id {
			output := o
			return &output
		}
	}
	return nil
}

// AddOutput adds a new output and saves the configuration.
func (c *Config) AddOutput(output types.Output) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if output.ID == "" {
		output.ID = fmt.Sprintf("output-%d", len(c.Outputs)+1)
	}
	if output.Codec == "" {
		output.Codec = "mp3"
	}
	output.CreatedAt = time.Now().UnixMilli()

	c.Outputs = append(c.Outputs, output)
	return c.saveLocked()
}

// RemoveOutput removes an output by ID and saves the configuration.
func (c *Config) RemoveOutput(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, o := range c.Outputs {
		if o.ID == id {
			c.Outputs = append(c.Outputs[:i], c.Outputs[i+1:]...)
			return c.saveLocked()
		}
	}
	return fmt.Errorf("output not found: %s", id)
}

// UpdateOutput updates an existing output and saves the configuration.
func (c *Config) UpdateOutput(output types.Output) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, o := range c.Outputs {
		if o.ID == output.ID {
			c.Outputs[i] = output
			return c.saveLocked()
		}
	}
	return fmt.Errorf("output not found: %s", output.ID)
}

// GetAudioInput returns the configured audio input device.
func (c *Config) GetAudioInput() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AudioInput
}

// SetAudioInput updates the audio input device and saves the configuration.
func (c *Config) SetAudioInput(input string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AudioInput = input
	return c.saveLocked()
}

// GetSilenceThreshold returns the configured silence threshold (default -40 dB).
func (c *Config) GetSilenceThreshold() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.SilenceThreshold == 0 {
		return DefaultSilenceThreshold
	}
	return c.SilenceThreshold
}

// SetSilenceThreshold updates the silence detection threshold and saves the configuration.
func (c *Config) SetSilenceThreshold(threshold float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceThreshold = threshold
	return c.saveLocked()
}

// GetSilenceDuration returns seconds of silence before alerting (default 15s).
func (c *Config) GetSilenceDuration() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.SilenceDuration == 0 {
		return DefaultSilenceDuration
	}
	return c.SilenceDuration
}

// SetSilenceDuration updates the silence duration and saves the configuration.
func (c *Config) SetSilenceDuration(seconds float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceDuration = seconds
	return c.saveLocked()
}

// GetSilenceRecovery returns seconds of audio before considering recovered (default 5s).
func (c *Config) GetSilenceRecovery() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.SilenceRecovery == 0 {
		return DefaultSilenceRecovery
	}
	return c.SilenceRecovery
}

// SetSilenceRecovery updates the silence recovery time and saves the configuration.
func (c *Config) SetSilenceRecovery(seconds float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceRecovery = seconds
	return c.saveLocked()
}

// GetSilenceWebhook returns the configured silence webhook URL.
func (c *Config) GetSilenceWebhook() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.SilenceWebhook
}

// SetSilenceWebhook updates the silence webhook URL and saves the configuration.
func (c *Config) SetSilenceWebhook(url string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceWebhook = url
	return c.saveLocked()
}

// GetEmailSMTPHost returns the configured SMTP host.
func (c *Config) GetEmailSMTPHost() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EmailSMTPHost
}

// GetEmailSMTPPort returns the configured SMTP port (default 587).
func (c *Config) GetEmailSMTPPort() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.EmailSMTPPort == 0 {
		return DefaultEmailSMTPPort
	}
	return c.EmailSMTPPort
}

// GetEmailUsername returns the configured SMTP username.
func (c *Config) GetEmailUsername() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EmailUsername
}

// GetEmailPassword returns the configured SMTP password.
func (c *Config) GetEmailPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EmailPassword
}

// GetEmailRecipients returns the configured email recipients.
func (c *Config) GetEmailRecipients() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EmailRecipients
}

// SetEmailConfig updates all email configuration fields and saves.
func (c *Config) SetEmailConfig(host string, port int, username, password, recipients string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.EmailSMTPHost = host
	c.EmailSMTPPort = port
	c.EmailUsername = username
	c.EmailPassword = password
	c.EmailRecipients = recipients
	return c.saveLocked()
}

// GetWebPort returns the web server port.
func (c *Config) GetWebPort() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.WebPort
}

// GetWebUser returns the web authentication username.
func (c *Config) GetWebUser() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.WebUser
}

// GetWebPassword returns the web authentication password.
func (c *Config) GetWebPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.WebPassword
}
