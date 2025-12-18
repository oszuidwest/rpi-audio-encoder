package recording

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// CleanupInterval is how often the cleanup runs.
const CleanupInterval = 1 * time.Hour

// Cleanup manages automatic deletion of old recording files for a single recording.
type Cleanup struct {
	recording *types.Recording
	stopChan  chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	running   bool
}

// NewCleanup creates a new Cleanup manager for a recording.
func NewCleanup(recording *types.Recording) *Cleanup {
	return &Cleanup{
		recording: recording,
		stopChan:  make(chan struct{}),
	}
}

// Start begins the cleanup goroutine.
func (c *Cleanup) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.stopChan = make(chan struct{})
	c.mu.Unlock()

	c.wg.Add(1)
	go c.run()

	slog.Info("recording cleanup started", "recording_id", c.recording.ID, "interval", CleanupInterval)
}

// Stop stops the cleanup goroutine.
func (c *Cleanup) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	close(c.stopChan)
	c.mu.Unlock()

	c.wg.Wait()
	slog.Info("recording cleanup stopped", "recording_id", c.recording.ID)
}

// run is the main cleanup loop.
func (c *Cleanup) run() {
	defer c.wg.Done()

	// Run immediately on start
	c.runCleanup()

	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			c.runCleanup()
		}
	}
}

// runCleanup performs the actual file cleanup.
func (c *Cleanup) runCleanup() {
	basePath := c.recording.Path
	retentionDays := c.recording.GetRetentionDays()

	if basePath == "" || retentionDays <= 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	slog.Debug("running recording cleanup", "recording_id", c.recording.ID, "path", basePath, "retention_days", retentionDays, "cutoff", cutoff.Format("2006-01-02"))

	// Check if base path exists
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return
	}

	// Walk through date directories
	entries, err := os.ReadDir(basePath)
	if err != nil {
		slog.Error("failed to read recording directory", "recording_id", c.recording.ID, "error", err)
		return
	}

	var deletedFiles, deletedDirs int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Parse date from directory name (format: 2006-01-02)
		dirDate, err := time.Parse("2006-01-02", entry.Name())
		if err != nil {
			// Not a date directory, skip
			continue
		}

		dirPath := filepath.Join(basePath, entry.Name())

		// If entire directory is older than retention, remove it
		if dirDate.Before(cutoff) {
			files, err := os.ReadDir(dirPath)
			if err == nil {
				deletedFiles += len(files)
			}

			if err := os.RemoveAll(dirPath); err != nil {
				slog.Error("failed to remove old recording directory", "recording_id", c.recording.ID, "path", dirPath, "error", err)
			} else {
				deletedDirs++
				slog.Debug("removed old recording directory", "recording_id", c.recording.ID, "path", dirPath)
			}
			continue
		}

		// For directories within retention, check individual files
		// (This handles edge cases where files might be older than their directory date)
		c.cleanupFilesInDir(dirPath, cutoff, &deletedFiles)

		// Remove directory if empty
		c.removeIfEmpty(dirPath, &deletedDirs)
	}

	if deletedFiles > 0 || deletedDirs > 0 {
		slog.Info("recording cleanup completed", "recording_id", c.recording.ID, "deleted_files", deletedFiles, "deleted_dirs", deletedDirs)
	}
}

// cleanupFilesInDir removes files older than cutoff in the given directory.
func (c *Cleanup) cleanupFilesInDir(dirPath string, cutoff time.Time, deletedFiles *int) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filePath := filepath.Join(dirPath, file.Name())
		info, err := file.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filePath); err != nil {
				slog.Error("failed to remove old recording file", "recording_id", c.recording.ID, "path", filePath, "error", err)
			} else {
				*deletedFiles++
				slog.Debug("removed old recording file", "recording_id", c.recording.ID, "path", filePath)
			}
		}
	}
}

// removeIfEmpty removes the directory if it's empty.
func (c *Cleanup) removeIfEmpty(dirPath string, deletedDirs *int) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return
	}

	if len(files) == 0 {
		if err := os.Remove(dirPath); err != nil {
			slog.Warn("failed to remove empty directory", "recording_id", c.recording.ID, "path", dirPath, "error", err)
		} else {
			*deletedDirs++
			slog.Debug("removed empty recording directory", "recording_id", c.recording.ID, "path", dirPath)
		}
	}
}

// CleanupManager manages cleanup for multiple recordings.
type CleanupManager struct {
	cleanups map[string]*Cleanup
	mu       sync.RWMutex
}

// NewCleanupManager creates a new cleanup manager.
func NewCleanupManager() *CleanupManager {
	return &CleanupManager{
		cleanups: make(map[string]*Cleanup),
	}
}

// Start starts cleanup for a recording.
func (m *CleanupManager) Start(recording *types.Recording) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If already exists, stop old one first
	if cleanup, exists := m.cleanups[recording.ID]; exists {
		cleanup.Stop()
	}

	cleanup := NewCleanup(recording)
	cleanup.Start()
	m.cleanups[recording.ID] = cleanup
}

// Stop stops cleanup for a specific recording.
func (m *CleanupManager) Stop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cleanup, exists := m.cleanups[id]; exists {
		cleanup.Stop()
		delete(m.cleanups, id)
	}
}

// StopAll stops all cleanup goroutines.
func (m *CleanupManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, cleanup := range m.cleanups {
		cleanup.Stop()
		delete(m.cleanups, id)
	}
}
