// Package encoder provides the audio capture and encoding engine.
package encoder

import (
	"log"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// AudioDevice represents an available audio input device.
type AudioDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListAudioDevices returns available audio input devices for the current platform.
func ListAudioDevices() []AudioDevice {
	switch runtime.GOOS {
	case "darwin":
		return listMacOSDevices()
	default:
		return listLinuxDevices()
	}
}

// listMacOSDevices returns available audio input devices on macOS.
func listMacOSDevices() []AudioDevice {
	cmd := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	// Note: ffmpeg -list_devices always returns non-zero exit code, so we ignore the error
	// The device list is still in the output even though the command "fails"
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		log.Printf("Failed to list macOS audio devices: %v", err)
		return nil
	}

	var devices []AudioDevice
	lines := strings.Split(string(output), "\n")
	inAudioSection := false

	// Pattern: [AVFoundation ...] [0] Device Name
	devicePattern := regexp.MustCompile(`\[AVFoundation[^\]]*\]\s*\[(\d+)\]\s*(.+)`)

	for _, line := range lines {
		if strings.Contains(line, "AVFoundation audio devices:") {
			inAudioSection = true
			continue
		}
		if strings.Contains(line, "AVFoundation video devices:") {
			inAudioSection = false
			continue
		}
		if inAudioSection {
			matches := devicePattern.FindStringSubmatch(line)
			if len(matches) == 3 {
				devices = append(devices, AudioDevice{
					ID:   ":" + matches[1],
					Name: strings.TrimSpace(matches[2]),
				})
			}
		}
	}

	return devices
}

// listLinuxDevices returns available audio input devices on Linux.
func listLinuxDevices() []AudioDevice {
	cmd := exec.Command("arecord", "-l")
	output, err := cmd.Output()
	if err != nil {
		// Fallback: return default HiFiBerry device
		return []AudioDevice{{
			ID:   "default:CARD=sndrpihifiberry",
			Name: "HiFiBerry (default)",
		}}
	}

	var devices []AudioDevice
	lines := strings.Split(string(output), "\n")

	// Pattern: card 0: sndrpihifiberry [snd_rpi_hifiberry_dac], device 0: ...
	cardPattern := regexp.MustCompile(`card\s+(\d+):\s+(\w+)\s+\[([^\]]+)\]`)

	for _, line := range lines {
		matches := cardPattern.FindStringSubmatch(line)
		if len(matches) == 4 {
			cardName := matches[2]
			description := matches[3]
			devices = append(devices, AudioDevice{
				ID:   "default:CARD=" + cardName,
				Name: description,
			})
		}
	}

	if len(devices) == 0 {
		// Fallback: return default device
		devices = append(devices, AudioDevice{
			ID:   "default",
			Name: "Default Audio Device",
		})
	}

	return devices
}
