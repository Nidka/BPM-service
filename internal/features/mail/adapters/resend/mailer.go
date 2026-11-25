// Package resend adapta el cliente HTTP de Resend al puerto Mailer de la feature de correo.
package resend

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	core "github.com/Nidka/BPM-service/internal/core/integrations/resend"
	"github.com/Nidka/BPM-service/internal/features/mail/application"
	"github.com/Nidka/BPM-service/internal/features/mail/domain"
)

// Mailer envía los mensajes renderizados con el remitente y la dirección de respuesta configurados.
type Mailer struct {
	client  *core.Client
	from    string
	replyTo string
}

// New crea el adaptador; con un cliente sin clave Configured devuelve false.
func New(client *core.Client, from, replyTo string) *Mailer {
	return &Mailer{client: client, from: from, replyTo: replyTo}
}

func (m *Mailer) Configured() bool { return m != nil && m.client.Configured() }

// Send entrega el correo. Cualquier fallo se traduce a application.ErrUnavailable para activar el respaldo del cliente.
func (m *Mailer) Send(ctx context.Context, to string, message domain.Message) error {
	if !m.Configured() {
		return application.ErrUnavailable
	}
	// Cada solicitud es una acción nueva de la persona: la clave de idempotencia solo protege reintentos internos del cliente HTTP.
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	_, err := m.client.Send(ctx, core.Email{
		From: m.from, To: to, ReplyTo: m.replyTo, Subject: message.Subject, HTML: message.HTML, Text: message.Text,
		IdempotencyKey: "bpm-mail/" + hex.EncodeToString(raw),
	})
	if err != nil {
		return application.ErrUnavailable
	}
	return nil
}

var _ application.Mailer = (*Mailer)(nil)
