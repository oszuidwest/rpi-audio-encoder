package encoder

import (
	"log/slog"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/oszuidwest/zwfm-encoder/internal/types"
)

// ListAudioDevices returns available audio input devices for the current platform.
func ListAudioDevices() []types.AudioDevice {
	switch runtime.GOOS {
	case "darwin":
		return listMacOSDevices()
	case "windows":
		return listWindowsDevices()
	default:
		return listLinuxDevices()
	}
}

// listMacOSDevices returns available audio input devices on macOS.
func listMacOSDevices() []types.AudioDevice {
	cmd := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "")
	// Note: ffmpeg -list_devices always returns non-zero exit code, so we ignore the error
	// The device list is still in the output even though the command "fails"
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		slog.Error("failed to list macOS audio devices", "error", err)
		return nil
	}

	var devices []types.AudioDevice
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
				devices = append(devices, types.AudioDevice{
					ID:   ":" + matches[1],
					Name: strings.TrimSpace(matches[2]),
				})
			}
		}
	}

	return devices
}

// listLinuxDevices returns available audio input devices on Linux.
func listLinuxDevices() []types.AudioDevice {
	cmd := exec.Command("arecord", "-l")
	output, err := cmd.Output()
	if err != nil {
		// Fallback: return default HiFiBerry device
		return []types.AudioDevice{{
			ID:   "default:CARD=sndrpihifiberry",
			Name: "HiFiBerry (default)",
		}}
	}

	var devices []types.AudioDevice
	lines := strings.Split(string(output), "\n")

	// Pattern: card 0: sndrpihifiberry [snd_rpi_hifiberry_dac], device 0: ...
	cardPattern := regexp.MustCompile(`card\s+(\d+):\s+(\w+)\s+\[([^\]]+)\]`)

	for _, line := range lines {
		matches := cardPattern.FindStringSubmatch(line)
		if len(matches) == 4 {
			cardName := matches[2]
			description := matches[3]
			devices = append(devices, types.AudioDevice{
				ID:   "default:CARD=" + cardName,
				Name: description,
			})
		}
	}

	if len(devices) == 0 {
		devices = append(devices, types.AudioDevice{
			ID:   "default",
			Name: "Default Audio Device",
		})
	}

	return devices
}

// listWindowsDevices returns available audio input devices on Windows.
func listWindowsDevices() []types.AudioDevice {
	cmd := exec.Command("ffmpeg", "-f", "dshow", "-list_devices", "true", "-i", "dummy")
	// Note: ffmpeg -list_devices always returns non-zero exit code, so we ignore the error.
	// The device list is still in the output even though the command "fails".
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		slog.Error("failed to list Windows audio devices", "error", err)
		return nil
	}

	var devices []types.AudioDevice
	lines := strings.Split(string(output), "\n")
	inAudioSection := false

	// Pattern: [dshow ...] "Device Name" (audio)
	// or: [dshow ...]  "Device Name"
	devicePattern := regexp.MustCompile(`\[dshow[^\]]*\]\s*"([^"]+)"`)

	for _, line := range lines {
		// Detect audio devices section.
		if strings.Contains(line, "DirectShow audio devices") {
			inAudioSection = true
			continue
		}
		// Stop at video devices section.
		if strings.Contains(line, "DirectShow video devices") {
			inAudioSection = false
			continue
		}
		// Skip alternative name lines.
		if strings.Contains(line, "Alternative name") {
			continue
		}
		if inAudioSection {
			matches := devicePattern.FindStringSubmatch(line)
			if len(matches) == 2 {
				deviceName := strings.TrimSpace(matches[1])
				devices = append(devices, types.AudioDevice{
					ID:   "audio=" + deviceName,
					Name: deviceName,
				})
			}
		}
	}

	// No fallback - return actual devices only.
	// Config will use first detected device or prompt user to configure.
	return devices
}
