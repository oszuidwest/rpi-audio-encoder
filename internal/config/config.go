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
	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// Default values.
const (
	DefaultWebPort          = 8080
	DefaultWebUsername      = "admin"
	DefaultWebPassword      = "encoder"
	DefaultSilenceThreshold = -40.0
	DefaultSilenceDuration  = 15.0 // seconds of silence before alerting
	DefaultSilenceRecovery  = 5.0  // seconds of audio before considering recovered
	DefaultEmailSMTPPort    = 587
	DefaultEmailFromName    = "ZuidWest FM Encoder"
)

// WebConfig contains web server configuration.
type WebConfig struct {
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// AudioConfig contains audio input configuration.
type AudioConfig struct {
	Input string `json:"input"`
}

// SilenceDetectionConfig contains silence detection configuration.
type SilenceDetectionConfig struct {
	ThresholdDB     float64 `json:"threshold_db,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	RecoverySeconds float64 `json:"recovery_seconds,omitempty"`
}

// EmailConfig contains email notification configuration.
type EmailConfig struct {
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	FromName   string `json:"from_name,omitempty"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Recipients string `json:"recipients,omitempty"`
}

// NotificationsConfig contains all notification configuration.
type NotificationsConfig struct {
	WebhookURL string      `json:"webhook_url,omitempty"`
	LogPath    string      `json:"log_path,omitempty"`
	Email      EmailConfig `json:"email,omitempty"`
}

// Config holds all application configuration. It is safe for concurrent use.
type Config struct {
	Web              WebConfig              `json:"web"`
	Audio            AudioConfig            `json:"audio"`
	SilenceDetection SilenceDetectionConfig `json:"silence_detection,omitempty"`
	Notifications    NotificationsConfig    `json:"notifications,omitempty"`
	Outputs          []types.Output         `json:"outputs"`

	mu       sync.RWMutex
	filePath string
}

// New creates a new Config with default values.
func New(filePath string) *Config {
	return &Config{
		Web: WebConfig{
			Port:     DefaultWebPort,
			Username: DefaultWebUsername,
			Password: DefaultWebPassword,
		},
		Audio: AudioConfig{
			Input: defaultAudioInput(),
		},
		SilenceDetection: SilenceDetectionConfig{},
		Notifications:    NotificationsConfig{},
		Outputs:          []types.Output{},
		filePath:         filePath,
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
		return util.WrapError("parse config", err)
	}

	c.applyDefaults()
	return nil
}

// applyDefaults sets default values for zero-value fields.
func (c *Config) applyDefaults() {
	if c.Web.Port == 0 {
		c.Web.Port = DefaultWebPort
	}
	if c.Web.Username == "" {
		c.Web.Username = DefaultWebUsername
	}
	if c.Web.Password == "" {
		c.Web.Password = DefaultWebPassword
	}
	if c.Audio.Input == "" {
		c.Audio.Input = defaultAudioInput()
	}
	if c.Outputs == nil {
		c.Outputs = []types.Output{}
	}
	for i := range c.Outputs {
		if c.Outputs[i].Codec == "" {
			c.Outputs[i].Codec = "mp3"
		}
		if c.Outputs[i].CreatedAt == 0 {
			c.Outputs[i].CreatedAt = time.Now().UnixMilli()
		}
	}
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
		return util.WrapError("marshal config", err)
	}

	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return util.WrapError("create config directory", err)
	}

	if err := os.WriteFile(c.filePath, data, 0600); err != nil {
		return util.WrapError("write config", err)
	}

	return nil
}

// defaultIfZero returns def if val equals the zero value of type T, otherwise returns val.
func defaultIfZero[T comparable](val, def T) T {
	var zero T
	if val == zero {
		return def
	}
	return val
}

// === Output Management ===

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

// findOutputIndex returns the index of the output with the given ID, or -1 if not found.
// Caller must hold c.mu (read or write lock).
func (c *Config) findOutputIndex(id string) int {
	for i, o := range c.Outputs {
		if o.ID == id {
			return i
		}
	}
	return -1
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

	i := c.findOutputIndex(id)
	if i == -1 {
		return fmt.Errorf("output not found: %s", id)
	}

	c.Outputs = append(c.Outputs[:i], c.Outputs[i+1:]...)
	return c.saveLocked()
}

// UpdateOutput updates an existing output and saves the configuration.
func (c *Config) UpdateOutput(output types.Output) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	i := c.findOutputIndex(output.ID)
	if i == -1 {
		return fmt.Errorf("output not found: %s", output.ID)
	}

	c.Outputs[i] = output
	return c.saveLocked()
}

// === Web Configuration ===

// GetWebPort returns the web server port.
func (c *Config) GetWebPort() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Web.Port
}

// GetWebUser returns the web authentication username.
func (c *Config) GetWebUser() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Web.Username
}

// GetWebPassword returns the web authentication password.
func (c *Config) GetWebPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Web.Password
}

// === Audio Configuration ===

// GetAudioInput returns the configured audio input device.
func (c *Config) GetAudioInput() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Audio.Input
}

// SetAudioInput updates the audio input device and saves the configuration.
func (c *Config) SetAudioInput(input string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Audio.Input = input
	return c.saveLocked()
}

// === Silence Detection Configuration ===

// GetSilenceThreshold returns the configured silence threshold (default -40 dB).
func (c *Config) GetSilenceThreshold() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return defaultIfZero(c.SilenceDetection.ThresholdDB, DefaultSilenceThreshold)
}

// SetSilenceThreshold updates the silence detection threshold and saves the configuration.
func (c *Config) SetSilenceThreshold(threshold float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceDetection.ThresholdDB = threshold
	return c.saveLocked()
}

// GetSilenceDuration returns seconds of silence before alerting (default 15s).
func (c *Config) GetSilenceDuration() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return defaultIfZero(c.SilenceDetection.DurationSeconds, DefaultSilenceDuration)
}

// SetSilenceDuration updates the silence duration and saves the configuration.
func (c *Config) SetSilenceDuration(seconds float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceDetection.DurationSeconds = seconds
	return c.saveLocked()
}

// GetSilenceRecovery returns seconds of audio before considering recovered (default 5s).
func (c *Config) GetSilenceRecovery() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return defaultIfZero(c.SilenceDetection.RecoverySeconds, DefaultSilenceRecovery)
}

// SetSilenceRecovery updates the silence recovery time and saves the configuration.
func (c *Config) SetSilenceRecovery(seconds float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SilenceDetection.RecoverySeconds = seconds
	return c.saveLocked()
}

// === Notifications Configuration ===

// GetWebhookURL returns the configured webhook URL for notifications.
func (c *Config) GetWebhookURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Notifications.WebhookURL
}

// SetWebhookURL updates the webhook URL and saves the configuration.
func (c *Config) SetWebhookURL(url string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Notifications.WebhookURL = url
	return c.saveLocked()
}

// GetLogPath returns the configured log file path for notifications.
func (c *Config) GetLogPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Notifications.LogPath
}

// SetLogPath updates the log file path and saves the configuration.
func (c *Config) SetLogPath(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Notifications.LogPath = path
	return c.saveLocked()
}

// === Email Configuration ===

// GetEmailSMTPHost returns the configured SMTP host.
func (c *Config) GetEmailSMTPHost() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Notifications.Email.Host
}

// GetEmailSMTPPort returns the configured SMTP port (default 587).
func (c *Config) GetEmailSMTPPort() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return defaultIfZero(c.Notifications.Email.Port, DefaultEmailSMTPPort)
}

// GetEmailFromName returns the configured email sender display name.
func (c *Config) GetEmailFromName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Notifications.Email.FromName == "" {
		return DefaultEmailFromName
	}
	return c.Notifications.Email.FromName
}

// GetEmailUsername returns the configured SMTP username.
func (c *Config) GetEmailUsername() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Notifications.Email.Username
}

// GetEmailPassword returns the configured SMTP password.
func (c *Config) GetEmailPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Notifications.Email.Password
}

// GetEmailRecipients returns the configured email recipients.
func (c *Config) GetEmailRecipients() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Notifications.Email.Recipients
}

// SetEmailConfig updates all email configuration fields and saves.
func (c *Config) SetEmailConfig(host string, port int, fromName, username, password, recipients string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Notifications.Email.Host = host
	c.Notifications.Email.Port = port
	c.Notifications.Email.FromName = fromName
	c.Notifications.Email.Username = username
	c.Notifications.Email.Password = password
	c.Notifications.Email.Recipients = recipients
	return c.saveLocked()
}
