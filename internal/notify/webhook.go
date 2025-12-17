package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// SendSilenceWebhook sends a POST request to the webhook URL when silence is critical.
func SendSilenceWebhook(webhookURL string, duration, threshold float64) error {
	return sendWebhook(webhookURL, map[string]any{
		"event":            "silence_detected",
		"silence_duration": duration,
		"threshold":        threshold,
		"timestamp":        util.RFC3339Now(),
	})
}

// SendRecoveryWebhook sends a POST request to the webhook URL when audio recovers.
func SendRecoveryWebhook(webhookURL string, silenceDuration float64) error {
	return sendWebhook(webhookURL, map[string]any{
		"event":            "silence_recovered",
		"silence_duration": silenceDuration,
		"timestamp":        util.RFC3339Now(),
	})
}

// SendTestWebhook sends a test POST request to verify webhook configuration.
func SendTestWebhook(webhookURL string) error {
	if webhookURL == "" {
		return fmt.Errorf("webhook URL not configured")
	}

	return sendWebhook(webhookURL, map[string]any{
		"event":     "test",
		"message":   "This is a test notification from ZuidWest FM Encoder",
		"timestamp": util.RFC3339Now(),
	})
}

// sendWebhook sends a POST request with JSON payload to the webhook URL.
func sendWebhook(webhookURL string, payload map[string]any) error {
	if !util.IsConfigured(webhookURL) {
		return nil // Silently skip if not configured
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return util.WrapError("marshal payload", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return util.WrapError("send webhook request", err)
	}
	defer util.SafeCloseFunc(resp.Body, "webhook response body")()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}
