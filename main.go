// Package main implements an audio streaming encoder that captures audio from digital input and streams to multiple SRT destinations.
//
// Usage:
//
//	encoder [-config path/to/config.json]
//
// If -config is not specified, the encoder looks for config.json in the same
// directory as the binary.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/encoder"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to config file (default: config.json next to binary)")
	showVersion := flag.Bool("version", false, "Print version information and exit")
	flag.Parse()

	if *showVersion {
		slog.Info("version info", "version", Version, "commit", Commit, "build_time", BuildTime)
		return
	}

	// Determine config path
	if *configPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			slog.Error("failed to get executable path", "error", err)
			os.Exit(1)
		}
		*configPath = filepath.Join(filepath.Dir(execPath), "config.json")
	}

	slog.Info("using config file", "path", *configPath)

	// Load configuration
	cfg := config.New(*configPath)
	if err := cfg.Load(); err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Create encoder
	enc := encoder.New(cfg)

	// Create HTTP server
	srv := NewServer(cfg, enc)

	// Always start encoder automatically
	slog.Info("starting encoder")
	if err := enc.Start(); err != nil {
		slog.Error("failed to start encoder", "error", err)
	}

	// Start web server (non-blocking, returns *http.Server)
	httpServer := srv.Start()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down")

	// Graceful HTTP server shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	// Stop encoder
	if err := enc.Stop(); err != nil {
		slog.Error("error stopping encoder", "error", err)
	}

	slog.Info("shutdown complete")
}
