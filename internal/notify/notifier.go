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

// NewSilenceNotifier creates a new silence notifier.
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
	threshold := n.cfg.GetSilenceThreshold()

	// Webhook notification
	n.mu.Lock()
	shouldSendWebhook := !n.webhookSent && n.isWebhookConfigured()
	if shouldSendWebhook {
		n.webhookSent = true
	}
	n.mu.Unlock()
	if shouldSendWebhook {
		go n.sendSilenceWebhook(duration)
	}

	// Email notification
	n.mu.Lock()
	shouldSendEmail := !n.emailSent && n.isEmailConfigured()
	if shouldSendEmail {
		n.emailSent = true
	}
	n.mu.Unlock()
	if shouldSendEmail {
		go n.sendSilenceEmail(duration)
	}

	// Log notification (independent of webhook/email)
	n.mu.Lock()
	shouldSendLog := !n.logSent && n.isLogConfigured()
	if shouldSendLog {
		n.logSent = true
	}
	n.mu.Unlock()
	if shouldSendLog {
		go n.logSilenceStart(threshold)
	}
}

// handleSilenceEnd triggers recovery notifications when silence ends.
func (n *SilenceNotifier) handleSilenceEnd(totalDuration float64) {
	threshold := n.cfg.GetSilenceThreshold()

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
		go n.logSilenceEnd(totalDuration, threshold)
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

// Configuration checks

func (n *SilenceNotifier) isWebhookConfigured() bool {
	return n.cfg.GetWebhookURL() != ""
}

func (n *SilenceNotifier) isEmailConfigured() bool {
	return n.cfg.GetEmailSMTPHost() != "" && n.cfg.GetEmailRecipients() != ""
}

func (n *SilenceNotifier) isLogConfigured() bool {
	return n.cfg.GetLogPath() != ""
}

// Notification senders

func (n *SilenceNotifier) sendSilenceWebhook(duration float64) {
	webhookURL := n.cfg.GetWebhookURL()
	threshold := n.cfg.GetSilenceThreshold()
	util.NotifyResultf(
		func() error { return SendSilenceWebhook(webhookURL, duration, threshold) },
		"Silence webhook",
		true,
	)
}

func (n *SilenceNotifier) sendRecoveryWebhook(duration float64) {
	webhookURL := n.cfg.GetWebhookURL()
	util.NotifyResultf(
		func() error { return SendRecoveryWebhook(webhookURL, duration) },
		"Recovery webhook",
		true,
	)
}

func (n *SilenceNotifier) sendSilenceEmail(duration float64) {
	cfg := n.buildEmailConfig()
	threshold := n.cfg.GetSilenceThreshold()
	util.NotifyResultf(
		func() error { return SendSilenceAlert(cfg, duration, threshold) },
		"Silence email",
		true,
	)
}

func (n *SilenceNotifier) sendRecoveryEmail(duration float64) {
	cfg := n.buildEmailConfig()
	util.NotifyResultf(
		func() error { return SendRecoveryAlert(cfg, duration) },
		"Recovery email",
		true,
	)
}

// buildEmailConfig constructs an EmailConfig from the current configuration.
func (n *SilenceNotifier) buildEmailConfig() EmailConfig {
	return EmailConfig{
		Host:       n.cfg.GetEmailSMTPHost(),
		Port:       n.cfg.GetEmailSMTPPort(),
		Username:   n.cfg.GetEmailUsername(),
		Password:   n.cfg.GetEmailPassword(),
		Recipients: n.cfg.GetEmailRecipients(),
	}
}

func (n *SilenceNotifier) logSilenceStart(threshold float64) {
	logPath := n.cfg.GetLogPath()
	util.NotifyResultf(
		func() error { return LogSilenceStart(logPath, threshold) },
		"Silence log",
		true,
	)
}

func (n *SilenceNotifier) logSilenceEnd(duration, threshold float64) {
	logPath := n.cfg.GetLogPath()
	util.NotifyResultf(
		func() error { return LogSilenceEnd(logPath, duration, threshold) },
		"Recovery log",
		true,
	)
}
