// Encoder captures audio from digital input and streams to multiple SRT destinations.
//
// Usage:
//
//	encoder [-config path/to/config.json]
//
// If -config is not specified, the encoder looks for config.json in the same
// directory as the binary.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to config file (default: config.json next to binary)")
	flag.Parse()

	// Determine config path
	if *configPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to get executable path: %v", err)
		}
		*configPath = filepath.Join(filepath.Dir(execPath), "config.json")
	}

	log.Printf("Using config file: %s", *configPath)

	// Load configuration
	config := NewConfig(*configPath)
	if err := config.Load(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create FFmpeg manager
	manager := NewFFmpegManager(config)

	// Create HTTP server
	server := NewServer(config, manager)

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")
		if err := manager.Stop(); err != nil {
			log.Printf("Error stopping encoder: %v", err)
		}
		os.Exit(0)
	}()

	// Always start encoder automatically
	log.Println("Starting encoder...")
	if err := manager.Start(); err != nil {
		log.Printf("Failed to start encoder: %v", err)
	}

	// Start web server (blocks)
	log.Fatal(server.Start())
}
