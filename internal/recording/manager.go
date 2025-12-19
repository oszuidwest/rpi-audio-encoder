// Package recording manages compliance audio recording to local files.
package recording

import (
	"log/slog"
	"sync"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// Manager coordinates multiple recording destinations.
type Manager struct {
	recorders map[string]*Recorder
	mu        sync.RWMutex
}

// NewManager creates a new recording manager.
func NewManager() *Manager {
	return &Manager{
		recorders: make(map[string]*Recorder),
	}
}

// Start starts a recording destination.
func (m *Manager) Start(recording *types.Recording, getRecording func() *types.Recording, encoderRunning func() bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If already exists and running, do nothing
	if rec, exists := m.recorders[recording.ID]; exists && rec.IsRunning() {
		return nil
	}

	// Create new recorder
	rec := NewRecorder(recording, getRecording, encoderRunning)
	if err := rec.Start(); err != nil {
		return err
	}

	m.recorders[recording.ID] = rec
	return nil
}

// Stop stops a recording by ID.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, exists := m.recorders[id]
	if !exists {
		return nil
	}

	if err := rec.Stop(); err != nil {
		slog.Warn("error stopping recording", "recording_id", id, "error", err)
	}

	delete(m.recorders, id)
	slog.Info("recording stopped", "recording_id", id)
	return nil
}

// StopAll stops all recordings.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, rec := range m.recorders {
		if err := rec.Stop(); err != nil {
			slog.Warn("error stopping recording", "recording_id", id, "error", err)
		}
	}
	m.recorders = make(map[string]*Recorder)
}

// WriteAudioAll writes PCM data to all running recordings.
func (m *Manager) WriteAudioAll(data []byte) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, rec := range m.recorders {
		if rec.IsRunning() {
			_ = rec.WriteAudio(data) //nolint:errcheck
		}
	}
}

// GetAllStatuses returns status for all tracked recordings.
func (m *Manager) GetAllStatuses() map[string]types.RecordingStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]types.RecordingStatus, len(m.recorders))
	for id, rec := range m.recorders {
		statuses[id] = *rec.GetStatus()
	}
	return statuses
}
