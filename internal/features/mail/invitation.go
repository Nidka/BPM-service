package mail

import (
	"context"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/config"
	coreresend "github.com/Nidka/BPM-service/internal/core/integrations/resend"
	"github.com/Nidka/BPM-service/internal/features/mail/domain"
)

// InvitationSender envía las invitaciones de organización con el mismo remitente y cliente de Resend que el resto del
// correo transaccional. Lo usa el worker (outbox); implementa el MailSender y el HTMLMailSender de organizaciones.
type InvitationSender struct {
	client  *coreresend.Client
	replyTo string
}

// NewInvitationSender crea el envío; el llamador solo lo usa si hay RESEND_API_KEY.
func NewInvitationSender(cfg config.EmailConfig) *InvitationSender {
	return &InvitationSender{client: coreresend.New(&http.Client{Timeout: 12 * time.Second}, cfg.ResendAPIKey), replyTo: cfg.ReplyTo}
}

// Send entrega solo texto (invitaciones sin URL de aceptación configurada).
func (s *InvitationSender) Send(ctx context.Context, key, from, to, subject, text string) (string, error) {
	return s.client.Send(ctx, coreresend.Email{From: from, To: to, ReplyTo: s.replyTo, Subject: subject, Text: text, IdempotencyKey: key})
}

// SendHTML entrega el correo de marca con texto alternativo.
func (s *InvitationSender) SendHTML(ctx context.Context, key, from, to, subject, text, html string) (string, error) {
	return s.client.Send(ctx, coreresend.Email{From: from, To: to, ReplyTo: s.replyTo, Subject: subject, Text: text, HTML: html, IdempotencyKey: key})
}

// RenderInvitation produce asunto, texto y HTML de la invitación; appURL es el origen público de la app (mascota y pie).
func RenderInvitation(appURL, email, organization, link, expiresAt string) (subject, text, html string) {
	m := domain.Invitation(domain.InvitationData{AppURL: appURL, Link: link, OrganizationName: organization, Email: email, ExpiresAt: expiresAt})
	return m.Subject, m.Text, m.HTML
}
