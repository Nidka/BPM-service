package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Nidka/BPM-service/internal/core/outbox"
)

// MailSender envía un correo de texto con clave de idempotencia estable.
type MailSender interface {
	Send(ctx context.Context, idempotencyKey, from, to, subject, text string) (string, error)
}

// HTMLMailSender es la variante con cuerpo HTML y texto alternativo. Si el MailSender configurado la implementa y hay una
// plantilla (WithTemplate), la invitación sale con la marca de BPM; si no, se envía el texto plano de siempre.
type HTMLMailSender interface {
	SendHTML(ctx context.Context, idempotencyKey, from, to, subject, text, html string) (string, error)
}

// InvitationContent son los datos que recibe la plantilla del correo de invitación.
type InvitationContent struct {
	Email, OrganizationName, Link, ExpiresAt string
}

// InvitationTemplate renderiza asunto, texto plano y HTML de la invitación.
type InvitationTemplate func(InvitationContent) (subject, text, html string)

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
	render  InvitationTemplate // nil: texto plano heredado
}

func NewInvitationMailer(store InvitationOutbox, sender MailSender, from, baseURL string) *InvitationMailer {
	return &InvitationMailer{store: store, sender: sender, from: from, baseURL: baseURL}
}

// WithTemplate activa el correo HTML de marca cuando el sender implementa HTMLMailSender.
func (m *InvitationMailer) WithTemplate(render InvitationTemplate) *InvitationMailer {
	m.render = render
	return m
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
	link := InvitationLink(m.baseURL, p.Token)
	if link == "" {
		link = p.Token
	}
	if html, ok := m.sender.(HTMLMailSender); ok && m.render != nil && link != p.Token {
		subject, text, body := m.render(InvitationContent{Email: p.Email, OrganizationName: p.OrganizationName, Link: link, ExpiresAt: raw["expiresAt"]})
		if _, err = html.SendHTML(ctx, "bpm-invitation/"+ev.DedupKey, m.from, p.Email, subject, text, body); err != nil {
			return err
		}
		return m.store.Scrub(ctx, ev.OrganizationID, ev.AggregateID)
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
