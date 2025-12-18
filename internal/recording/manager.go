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
func (m *Manager) Start(recording *types.Recording) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If already exists and running, do nothing
	if rec, exists := m.recorders[recording.ID]; exists && rec.IsRunning() {
		return nil
	}

	// Create new recorder
	rec := NewRecorder(recording)
	if err := rec.Start(); err != nil {
		return err
	}

	m.recorders[recording.ID] = rec
	// Note: Recorder.Start() logs the start event, no duplicate logging here
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

// WriteAudio writes PCM data to a specific recording.
func (m *Manager) WriteAudio(id string, data []byte) error {
	m.mu.RLock()
	rec, exists := m.recorders[id]
	m.mu.RUnlock()

	if !exists || !rec.IsRunning() {
		return nil
	}

	return rec.WriteAudio(data)
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

// GetStatus returns status for a specific recording.
func (m *Manager) GetStatus(id string) *types.RecordingStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.recorders[id]
	if !exists {
		return &types.RecordingStatus{Running: false}
	}

	return rec.GetStatus()
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

// IsRunning returns whether a specific recording is active.
func (m *Manager) IsRunning(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, exists := m.recorders[id]
	return exists && rec.IsRunning()
}
