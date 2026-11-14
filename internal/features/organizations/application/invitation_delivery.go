package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/Nidka/BPM-service/internal/core/outbox"
)

// MailSender envía un correo de texto con clave de idempotencia estable.
type MailSender interface {
	Send(ctx context.Context, idempotencyKey, from, to, subject, text string) (string, error)
}

// InvitationOutbox verifica que el evento siga vigente y borra el token tras el envío.
type InvitationOutbox interface {
	// Current indica si la invitación sigue pendiente, vigente y con ese hash de token.
	Current(ctx context.Context, org, id, tokenHash string) (bool, error)
	// Scrub elimina el token sin cifrar del payload del outbox.
	Scrub(ctx context.Context, org, id string) error
}

// InvitationMailer entrega los correos de invitación desde el worker.
type InvitationMailer struct {
	store   InvitationOutbox
	sender  MailSender // nil si el proveedor no está configurado
	from    string
	baseURL string
}

func NewInvitationMailer(store InvitationOutbox, sender MailSender, from, baseURL string) *InvitationMailer {
	return &InvitationMailer{store, sender, from, baseURL}
}

// Handle procesa organizations.invitation.requested.v1. Un evento sustituido,
// revocado, aceptado o vencido se descarta sin enviar nada.
func (m *InvitationMailer) Handle(ctx context.Context, ev outbox.Event) error {
	if m.sender == nil {
		return outbox.ErrUnavailable
	}
	var p struct {
		Email, OrganizationName, Token string
	}
	var raw map[string]string
	if err := json.Unmarshal(ev.Payload, &raw); err != nil {
		return nil
	}
	p.Email, p.OrganizationName, p.Token = raw["email"], raw["organizationName"], raw["token"]
	if p.Token == "" || p.Email == "" {
		return nil
	}
	ok, err := m.store.Current(ctx, ev.OrganizationID, ev.AggregateID, HashInvitationToken(p.Token))
	if err != nil {
		return err
	}
	if !ok {
		return m.store.Scrub(ctx, ev.OrganizationID, ev.AggregateID)
	}
	link := p.Token
	if u, e := url.Parse(m.baseURL); m.baseURL != "" && e == nil {
		q := u.Query()
		q.Set("token", p.Token)
		u.RawQuery = q.Encode()
		link = u.String()
	}
	text := "Te invitaron a unirte a " + p.OrganizationName + " en BPM.\n\n" +
		"Inicia sesión con este correo (" + p.Email + ") y acepta la invitación aquí:\n" + link + "\n\n" +
		"La invitación vence el " + raw["expiresAt"] + " y solo puede usarse una vez. Si no la esperabas, ignora este mensaje."
	if _, err = m.sender.Send(ctx, "bpm-invitation/"+ev.DedupKey, m.from, p.Email, "Invitación a "+strings.TrimSpace(p.OrganizationName)+" en BPM", text); err != nil {
		if errors.Is(err, outbox.ErrUnavailable) {
			return err
		}
		return err
	}
	return m.store.Scrub(ctx, ev.OrganizationID, ev.AggregateID)
}
