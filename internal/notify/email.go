// Package notify provides notification services for silence alerts.
package notify

import (
	"fmt"
	"strings"

	"github.com/oszuidwest/zwfm-encoder/internal/util"
	"github.com/wneessen/go-mail"
)

// EmailConfig holds SMTP configuration.
type EmailConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	Recipients string
}

// SendSilenceAlert sends an email notification for critical silence.
func SendSilenceAlert(cfg EmailConfig, duration, threshold float64) error {
	if !util.IsConfigured(cfg.Host, cfg.Username, cfg.Recipients) {
		return nil // Silently skip if not configured
	}

	subject := "⚠️ ZuidWest FM Encoder: Silence Detected"
	body := fmt.Sprintf(
		"Critical silence detected on audio encoder.\n\n"+
			"Duration: %.1f seconds\n"+
			"Threshold: %.1f dB\n"+
			"Time: %s\n\n"+
			"Please check the audio source.",
		duration, threshold, util.RFC3339Now(),
	)

	return sendEmail(cfg, subject, body)
}

// SendRecoveryAlert sends an email notification when audio recovers from silence.
func SendRecoveryAlert(cfg EmailConfig, silenceDuration float64) error {
	if !util.IsConfigured(cfg.Host, cfg.Username, cfg.Recipients) {
		return nil // Silently skip if not configured
	}

	subject := "✅ ZuidWest FM Encoder: Audio Recovered"
	body := fmt.Sprintf(
		"Audio has recovered on the encoder.\n\n"+
			"Silence duration: %.1f seconds\n"+
			"Time: %s",
		silenceDuration, util.RFC3339Now(),
	)

	return sendEmail(cfg, subject, body)
}

// SendTestEmail sends a test email to verify SMTP configuration.
func SendTestEmail(cfg EmailConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("SMTP host not configured")
	}
	if cfg.Username == "" {
		return fmt.Errorf("email username not configured")
	}
	if cfg.Recipients == "" {
		return fmt.Errorf("email recipients not configured")
	}

	subject := "ZuidWest FM Encoder: Test Email"
	body := fmt.Sprintf(
		"This is a test email from your ZuidWest FM Encoder.\n\n"+
			"Time: %s\n\n"+
			"If you received this email, your SMTP configuration is working correctly.",
		util.RFC3339Now(),
	)

	return sendEmail(cfg, subject, body)
}

// sendEmail sends an email using go-mail with STARTTLS.
func sendEmail(cfg EmailConfig, subject, body string) error {
	// Parse recipients (comma-separated)
	recipients := strings.Split(cfg.Recipients, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	// Create message
	m := mail.NewMsg()
	if err := m.From(cfg.Username); err != nil {
		return util.WrapError("set from address", err)
	}
	if err := m.To(recipients...); err != nil {
		return util.WrapError("set recipient address", err)
	}
	m.Subject(subject)
	m.SetBodyString(mail.TypeTextPlain, body)

	// Create client with STARTTLS
	c, err := mail.NewClient(cfg.Host,
		mail.WithPort(cfg.Port),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithUsername(cfg.Username),
		mail.WithPassword(cfg.Password),
		mail.WithTLSPortPolicy(mail.TLSMandatory),
	)
	if err != nil {
		return util.WrapError("create SMTP client", err)
	}

	if err := c.DialAndSend(m); err != nil {
		return util.WrapError("send email", err)
	}

	return nil
}
