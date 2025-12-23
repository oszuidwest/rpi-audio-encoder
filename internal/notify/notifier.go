package notify

import (
	"sync"

	"github.com/oszuidwest/zwfm-encoder/internal/audio"
	"github.com/oszuidwest/zwfm-encoder/internal/config"
	"github.com/oszuidwest/zwfm-encoder/internal/util"
)

// SilenceNotifier orchestrates notifications for silence events.
// It tracks which notifications have been sent to avoid duplicates,
// and independently triggers webhook, email, and log notifications
// based on configuration.
//
// This separates notification concerns from the SilenceDetector,
// which only handles pure audio level detection.
type SilenceNotifier struct {
	cfg *config.Config

	// mu protects the notification state fields below
	mu sync.Mutex

	// Track which notifications have been sent for current silence period
	webhookSent bool
	emailSent   bool
	logSent     bool
}

// NewSilenceNotifier returns a SilenceNotifier configured with the given config.
func NewSilenceNotifier(cfg *config.Config) *SilenceNotifier {
	return &SilenceNotifier{cfg: cfg}
}

// HandleEvent processes a silence event and triggers appropriate notifications.
// Call this after each SilenceDetector.Update() with the returned event.
func (n *SilenceNotifier) HandleEvent(event audio.SilenceEvent) {
	if event.JustEntered {
		n.handleSilenceStart(event.Duration)
	}

	if event.JustRecovered {
		n.handleSilenceEnd(event.TotalDuration)
	}
}

// handleSilenceStart triggers notifications when silence is first detected.
func (n *SilenceNotifier) handleSilenceStart(duration float64) {
	// Single snapshot for all config checks
	cfg := n.cfg.Snapshot()

	n.mu.Lock()
	shouldSendWebhook := !n.webhookSent && cfg.HasWebhook()
	if shouldSendWebhook {
		n.webhookSent = true
	}
	n.mu.Unlock()
	if shouldSendWebhook {
		go n.sendSilenceWebhook(duration)
	}

	n.mu.Lock()
	shouldSendEmail := !n.emailSent && cfg.HasEmail()
	if shouldSendEmail {
		n.emailSent = true
	}
	n.mu.Unlock()
	if shouldSendEmail {
		go n.sendSilenceEmail(duration)
	}

	// Log notification (independent of webhook/email)
	n.mu.Lock()
	shouldSendLog := !n.logSent && cfg.HasLogPath()
	if shouldSendLog {
		n.logSent = true
	}
	n.mu.Unlock()
	if shouldSendLog {
		go n.logSilenceStart(cfg.SilenceThreshold)
	}
}

// handleSilenceEnd triggers recovery notifications when silence ends.
func (n *SilenceNotifier) handleSilenceEnd(totalDuration float64) {
	cfg := n.cfg.Snapshot()

	// Only send recovery notifications if we sent the corresponding start notification
	n.mu.Lock()
	shouldSendWebhookRecovery := n.webhookSent
	shouldSendEmailRecovery := n.emailSent
	shouldSendLogRecovery := n.logSent
	// Reset notification state for next silence period
	n.webhookSent = false
	n.emailSent = false
	n.logSent = false
	n.mu.Unlock()

	if shouldSendWebhookRecovery {
		go n.sendRecoveryWebhook(totalDuration)
	}

	if shouldSendEmailRecovery {
		go n.sendRecoveryEmail(totalDuration)
	}

	if shouldSendLogRecovery {
		go n.logSilenceEnd(totalDuration, cfg.SilenceThreshold)
	}
}

// Reset clears the notification state.
func (n *SilenceNotifier) Reset() {
	n.mu.Lock()
	n.webhookSent = false
	n.emailSent = false
	n.logSent = false
	n.mu.Unlock()
}

// Notification senders.

func (n *SilenceNotifier) sendSilenceWebhook(duration float64) {
	cfg := n.cfg.Snapshot()
	util.NotifyResultf(
		func() error { return SendSilenceWebhook(cfg.WebhookURL, duration, cfg.SilenceThreshold) },
		"Silence webhook",
		true,
	)
}

func (n *SilenceNotifier) sendRecoveryWebhook(duration float64) {
	cfg := n.cfg.Snapshot()
	util.NotifyResultf(
		func() error { return SendRecoveryWebhook(cfg.WebhookURL, duration) },
		"Recovery webhook",
		true,
	)
}

func (n *SilenceNotifier) sendSilenceEmail(duration float64) {
	cfg := n.cfg.Snapshot()
	emailCfg := EmailConfig{
		Host:       cfg.EmailSMTPHost,
		Port:       cfg.EmailSMTPPort,
		Username:   cfg.EmailUsername,
		Password:   cfg.EmailPassword,
		Recipients: cfg.EmailRecipients,
	}
	util.NotifyResultf(
		func() error { return SendSilenceAlert(&emailCfg, duration, cfg.SilenceThreshold) },
		"Silence email",
		true,
	)
}

func (n *SilenceNotifier) sendRecoveryEmail(duration float64) {
	cfg := n.cfg.Snapshot()
	emailCfg := EmailConfig{
		Host:       cfg.EmailSMTPHost,
		Port:       cfg.EmailSMTPPort,
		Username:   cfg.EmailUsername,
		Password:   cfg.EmailPassword,
		Recipients: cfg.EmailRecipients,
	}
	util.NotifyResultf(
		func() error { return SendRecoveryAlert(&emailCfg, duration) },
		"Recovery email",
		true,
	)
}

func (n *SilenceNotifier) logSilenceStart(threshold float64) {
	cfg := n.cfg.Snapshot()
	util.NotifyResultf(
		func() error { return LogSilenceStart(cfg.LogPath, threshold) },
		"Silence log",
		true,
	)
}

func (n *SilenceNotifier) logSilenceEnd(duration, threshold float64) {
	cfg := n.cfg.Snapshot()
	util.NotifyResultf(
		func() error { return LogSilenceEnd(cfg.LogPath, duration, threshold) },
		"Recovery log",
		true,
	)
}
