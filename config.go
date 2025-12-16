package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Config holds all application configuration. It is safe for concurrent use.
type Config struct {
	WebPort     int      `json:"web_port"`
	WebUser     string   `json:"web_user"`
	WebPassword string   `json:"web_password"`
	AudioInput  string   `json:"audio_input"`
	Outputs     []Output `json:"outputs"`

	mu       sync.RWMutex
	filePath string
}

// NewConfig creates a new Config with default values.
func NewConfig(filePath string) *Config {
	return &Config{
		WebPort:     8080,
		WebUser:     "admin",
		WebPassword: "encoder",
		AudioInput:  defaultAudioInput(),
		Outputs:     []Output{},
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
		// Create default config
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

// GetOutputs returns a copy of all outputs.
func (c *Config) GetOutputs() []Output {
	c.mu.RLock()
	defer c.mu.RUnlock()

	outputs := make([]Output, len(c.Outputs))
	copy(outputs, c.Outputs)
	return outputs
}

// GetOutput returns a copy of the output with the given ID, or nil if not found.
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

// AddOutput adds a new output and saves the configuration.
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

	// Set creation timestamp (Unix millis for easy JS comparison)
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
func (c *Config) UpdateOutput(output Output) error {
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
