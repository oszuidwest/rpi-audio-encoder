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

	subject := "[ALERT] Silence Detected - ZuidWest FM Encoder"
	body := fmt.Sprintf(
		"Silence detected on the audio encoder.\n\n"+
			"Duration:  %.1f seconds\n"+
			"Threshold: %.1f dB\n"+
			"Time:      %s\n\n"+
			"Please check the audio source.",
		duration, threshold, util.HumanTime(),
	)

	return sendEmail(cfg, subject, body)
}

// SendRecoveryAlert sends an email notification when audio recovers from silence.
func SendRecoveryAlert(cfg EmailConfig, silenceDuration float64) error {
	if !util.IsConfigured(cfg.Host, cfg.Username, cfg.Recipients) {
		return nil // Silently skip if not configured
	}

	subject := "[OK] Audio Recovered - ZuidWest FM Encoder"
	body := fmt.Sprintf(
		"Audio recovered on the encoder.\n\n"+
			"Silence lasted: %.1f seconds\n"+
			"Time:           %s",
		silenceDuration, util.HumanTime(),
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

	subject := "[TEST] ZuidWest FM Encoder"
	body := fmt.Sprintf(
		"Test email from the audio encoder.\n\n"+
			"Time: %s\n\n"+
			"SMTP configuration is working correctly.",
		util.HumanTime(),
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
