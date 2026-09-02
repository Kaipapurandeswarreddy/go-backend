package mailer

import (
	"fmt"

	"ambigo-backend/config"
	"ambigo-backend/internal/logger"

	"github.com/resend/resend-go/v3"
)

type ResendMailer struct {
	client       *resend.Client
	fromEmail    string
	fromName     string
	toTest       string
	useVerified  bool
}

func NewResendMailer(cfg *config.AppConfig) *ResendMailer {
	if cfg.ResendAPIKey == "" {
		logger.Log.Warn().Msg("RESEND_API_KEY empty — receptionist invite emails will be logged only")
		return &ResendMailer{
			fromEmail:   cfg.ResendFromEmail,
			fromName:    cfg.ResendFromName,
			toTest:      cfg.ResendToTest,
			useVerified: cfg.ResendUseVerified,
		}
	}
	return &ResendMailer{
		client:      resend.NewClient(cfg.ResendAPIKey),
		fromEmail:   cfg.ResendFromEmail,
		fromName:    cfg.ResendFromName,
		toTest:      cfg.ResendToTest,
		useVerified: cfg.ResendUseVerified,
	}
}

// SendReceptionistInvite sends temp credentials. In test mode (useVerified=false) it sends to RESEND_TO_TEST
// and logs the real recipient to avoid Domain not verified errors while ambigo.in is verifying.
func (m *ResendMailer) SendReceptionistInvite(realEmail, username, tempPassword, hospitalName string) error {
	if m.client == nil {
		logger.Log.Info().Str("to", realEmail).Str("username", username).Msg("[RESEND TEST MODE] Would send invite email (no API key)")
		return nil
	}
	to := realEmail
	if !m.useVerified {
		to = m.toTest
		if to == "" {
			to = "delivered@resend.dev"
		}
		logger.Log.Info().Str("real_to", realEmail).Str("test_to", to).Str("username", username).Msg("Resend test mode: redirecting email to test inbox")
	}
	from := fmt.Sprintf("%s <%s>", m.fromName, m.fromEmail)
	subject := fmt.Sprintf("Your %s receptionist account", hospitalName)
	html := fmt.Sprintf(`
		<div style="font-family: sans-serif; max-width: 600px; margin: 0 auto;">
			<h2 style="color: #FF520D;">Welcome to Ambigo</h2>
			<p>Your receptionist account has been created for <b>%s</b>.</p>
			<table style="background: #f8f8f8; padding: 16px; border-radius: 8px; width: 100%%;">
				<tr><td><b>Username</b></td><td>%s</td></tr>
				<tr><td><b>Temporary password</b></td><td><code style="background: #fff; padding: 4px 8px; border-radius: 4px;">%s</code></td></tr>
				<tr><td><b>Login</b></td><td><a href="https://hospital.ambigo.in" style="color: #FF520D;">https://hospital.ambigo.in</a></td></tr>
			</table>
			<p style="color: #666; font-size: 12px;">You will be asked to change your password on first login.</p>
			<p style="color: #666; font-size: 12px;">If not in test mode, original recipient: %s</p>
		</div>
	`, hospitalName, username, tempPassword, realEmail)

	params := &resend.SendEmailRequest{
		From:    from,
		To:      []string{to},
		Subject: subject,
		Html:    html,
	}
	sent, err := m.client.Emails.Send(params)
	if err != nil {
		logger.Log.Error().Err(err).Str("to", to).Str("real_to", realEmail).Msg("Resend send failed")
		return err
	}
	logger.Log.Info().Str("id", sent.Id).Str("to", to).Str("real_to", realEmail).Msg("Resend invite sent")
	return nil
}
