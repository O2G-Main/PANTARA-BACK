package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// DevSMTPMailer delivers SMTP mail when configured, or emits safe dev logs when SMTP_HOST is empty.
type DevSMTPMailer struct {
	Host string
	Port string
	User string
	Pass string
	From string
	Log  *slog.Logger
}

func (m *DevSMTPMailer) SendInvitation(ctx context.Context, toEmail, toName, orgName, inviterName, acceptURL string) error {
	subject := fmt.Sprintf("Undangan bergabung ke %s di Pantara", orgName)
	body := fmt.Sprintf("Halo %s,\n\n%s mengundangmu bergabung ke ruang kerja %s.\nBuka tautan berikut untuk membuat sandi dan mengisi profil kerjamu:\n%s\n",
		toName, inviterName, orgName, acceptURL)
	return m.deliver(toEmail, subject, body)
}

func (m *DevSMTPMailer) SendPasswordReset(ctx context.Context, toEmail, resetURL string) error {
	subject := "Buat sandi baru Pantara"
	body := fmt.Sprintf("Klik tautan berikut untuk membuat sandi baru (berlaku 30 menit):\n%s\n", resetURL)
	return m.deliver(toEmail, subject, body)
}

func (m *DevSMTPMailer) deliver(to, subject, body string) error {
	if strings.TrimSpace(m.Host) == "" {
		if m.Log != nil {
			m.Log.Info("dev_mail_logged", "to", to, "subject", subject, "body", body)
		}
		return nil
	}
	addr := fmt.Sprintf("%s:%s", m.Host, m.Port)
	var auth smtp.Auth
	if m.User != "" {
		auth = smtp.PlainAuth("", m.User, m.Pass, m.Host)
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		m.From, to, subject, body)
	return smtp.SendMail(addr, auth, m.From, []string{to}, []byte(msg))
}
