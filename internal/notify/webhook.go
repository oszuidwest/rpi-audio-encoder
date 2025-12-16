package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// SendSilenceWebhook sends a POST request to the webhook URL when silence is critical.
func SendSilenceWebhook(webhookURL string, duration, threshold float64) error {
	return sendWebhook(webhookURL, map[string]any{
		"event":            "silence_detected",
		"silence_duration": duration,
		"threshold":        threshold,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	})
}

// SendRecoveryWebhook sends a POST request to the webhook URL when audio recovers.
func SendRecoveryWebhook(webhookURL string, silenceDuration float64) error {
	return sendWebhook(webhookURL, map[string]any{
		"event":            "silence_recovered",
		"silence_duration": silenceDuration,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	})
}

// sendWebhook sends a POST request with JSON payload to the webhook URL.
func sendWebhook(webhookURL string, payload map[string]any) error {
	if webhookURL == "" {
		return nil // Silently skip if not configured
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close webhook response body: %v", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}
