package main

import (
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Config holds all application configuration
type Config struct {
	WebPort     int      `json:"web_port"`
	WebUser     string   `json:"web_user"`
	WebPassword string   `json:"web_password"`
	AutoStart   bool     `json:"auto_start"`
	AudioInput  string   `json:"audio_input"`
	Outputs     []Output `json:"outputs"`

	mu       sync.RWMutex
	filePath string
}

// NewConfig creates a new config with defaults
func NewConfig(filePath string) *Config {
	return &Config{
		WebPort:     8080,
		WebUser:     "admin",
		WebPassword: "encoder",
		AutoStart:   true,
		AudioInput:  defaultAudioInput(),
		Outputs:     []Output{},
		filePath:    filePath,
	}
}

// defaultAudioInput returns the default audio input device for the current platform
func defaultAudioInput() string {
	if runtime.GOOS == "darwin" {
		return ":0"
	}
	return "default:CARD=sndrpihifiberry"
}

// Load reads config from file, creates default if not exists
func (c *Config) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if os.IsNotExist(err) {
		// Create default config
		return c.saveUnsafe()
	}
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Migrate old configs: set default codec for outputs without one
	for i := range c.Outputs {
		if c.Outputs[i].Codec == "" {
			c.Outputs[i].Codec = "mp3"
		}
	}

	// Migrate old configs: set default audio input if not set
	if c.AudioInput == "" {
		c.AudioInput = defaultAudioInput()
	}

	return nil
}

// Save writes config to file
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveUnsafe()
}

// saveUnsafe saves without locking (caller must hold lock)
func (c *Config) saveUnsafe() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(c.filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// GetOutputs returns a copy of all outputs
func (c *Config) GetOutputs() []Output {
	c.mu.RLock()
	defer c.mu.RUnlock()

	outputs := make([]Output, len(c.Outputs))
	copy(outputs, c.Outputs)
	return outputs
}

// GetEnabledOutputs returns only enabled outputs
func (c *Config) GetEnabledOutputs() []Output {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var outputs []Output
	for _, o := range c.Outputs {
		if o.Enabled {
			outputs = append(outputs, o)
		}
	}
	return outputs
}

// EnabledOutputs returns an iterator over enabled outputs for use in range loops.
func (c *Config) EnabledOutputs() iter.Seq[Output] {
	return func(yield func(Output) bool) {
		c.mu.RLock()
		defer c.mu.RUnlock()
		for _, o := range c.Outputs {
			if o.Enabled {
				if !yield(o) {
					return
				}
			}
		}
	}
}

// GetOutput returns a single output by ID
func (c *Config) GetOutput(id string) *Output {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, o := range c.Outputs {
		if o.ID == id {
			output := o // Create a copy
			return &output
		}
	}
	return nil
}

// AddOutput adds a new output and saves config
func (c *Config) AddOutput(output Output) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Generate ID if not provided
	if output.ID == "" {
		output.ID = fmt.Sprintf("output-%d", len(c.Outputs)+1)
	}

	// Set default codec if not provided
	if output.Codec == "" {
		output.Codec = "mp3"
	}

	c.Outputs = append(c.Outputs, output)
	return c.saveUnsafe()
}

// RemoveOutput removes an output by ID and saves config
func (c *Config) RemoveOutput(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, o := range c.Outputs {
		if o.ID == id {
			c.Outputs = append(c.Outputs[:i], c.Outputs[i+1:]...)
			return c.saveUnsafe()
		}
	}
	return fmt.Errorf("output not found: %s", id)
}

// UpdateOutput updates an existing output
func (c *Config) UpdateOutput(output Output) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, o := range c.Outputs {
		if o.ID == output.ID {
			c.Outputs[i] = output
			return c.saveUnsafe()
		}
	}
	return fmt.Errorf("output not found: %s", output.ID)
}

// GetAudioInput returns the configured audio input device
func (c *Config) GetAudioInput() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AudioInput
}

// SetAudioInput updates the audio input device and saves config
func (c *Config) SetAudioInput(input string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AudioInput = input
	return c.saveUnsafe()
}
