package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/outbox"
)

type fakeOutbox struct {
	current bool
	scrubs  int
}

func (f *fakeOutbox) Current(context.Context, string, string, string) (bool, error) {
	return f.current, nil
}
func (f *fakeOutbox) Scrub(context.Context, string, string) error { f.scrubs++; return nil }

type fakeMail struct {
	to, text string
	calls    int
}

func (f *fakeMail) Send(_ context.Context, _, _, to, _, text string) (string, error) {
	f.calls++
	f.to, f.text = to, text
	return "id", nil
}

func event() outbox.Event {
	return outbox.Event{OrganizationID: "o", AggregateID: "i", DedupKey: "invitation/i/1", Payload: []byte(`{"email":"a@example.com","organizationName":"Uno","token":"tok","expiresAt":"2026-01-01T00:00:00Z"}`)}
}

func TestInvitationMailer(t *testing.T) {
	store, mail := &fakeOutbox{current: true}, &fakeMail{}
	if e := NewInvitationMailer(store, mail, "BPM <x@example.com>", "https://app.example.com/aceptar").Handle(context.Background(), event()); e != nil {
		t.Fatal(e)
	}
	if mail.to != "a@example.com" || !strings.Contains(mail.text, "https://app.example.com/aceptar?token=tok") || store.scrubs != 1 {
		t.Fatal("mail not sent or token not scrubbed", mail, store)
	}
	// Evento sustituido: no se envía nada y se borra el token.
	store, mail = &fakeOutbox{}, &fakeMail{}
	if e := NewInvitationMailer(store, mail, "x", "").Handle(context.Background(), event()); e != nil || mail.calls != 0 || store.scrubs != 1 {
		t.Fatal("stale event sent", e, mail, store)
	}
	// Proveedor sin configurar: se difiere.
	if e := NewInvitationMailer(store, nil, "x", "").Handle(context.Background(), event()); !errors.Is(e, outbox.ErrUnavailable) {
		t.Fatal("unavailable provider", e)
	}
}

func TestInvitationTokenIsRandomAndHashed(t *testing.T) {
	a, ha, _ := NewInvitationToken()
	b, _, _ := NewInvitationToken()
	if a == b || len(a) != 43 || ha != HashInvitationToken(a) || ha == a || !tokenPattern.MatchString(a) {
		t.Fatal("invalid token")
	}
}

func TestAccountInputValidationNeverAccessesDatabase(t *testing.T) {
	s := New(nil)
	for name, call := range map[string]func() error{
		"member role": func() error {
			_, e := s.UpdateMember(context.Background(), "u", "o", "t", MemberUpdate{Role: "ROOT", RowVersion: 1})
			return e
		},
		"member ver": func() error { _, e := s.DeactivateMember(context.Background(), "u", "o", "t", 0); return e },
		"invite":     func() error { _, _, e := s.Invite(context.Background(), "u", "o", InvitationInput{}); return e },
		"transfer self": func() error {
			_, _, e := s.TransferAdmin(context.Background(), "u", "o", "u", TransferInput{RowVersion: 1})
			return e
		},
	} {
		if e := call(); e == nil || !strings.Contains(e.Error(), "invalid") {
			t.Fatal(name, e)
		}
	}
}

type htmlMail struct {
	fakeMail
	subject, html string
}

func (f *htmlMail) SendHTML(_ context.Context, _, _, to, subject, text, html string) (string, error) {
	f.calls++
	f.to, f.text, f.subject, f.html = to, text, subject, html
	return "id", nil
}

func TestInvitationMailerUsesHTMLTemplateWhenAvailable(t *testing.T) {
	store, mail := &fakeOutbox{current: true}, &htmlMail{}
	tpl := func(c InvitationContent) (string, string, string) {
		return "Asunto " + c.OrganizationName, "texto " + c.Link, "<p>" + c.Link + "</p>"
	}
	if e := NewInvitationMailer(store, mail, "x", "https://app.example.com/aceptar").WithTemplate(tpl).Handle(context.Background(), event()); e != nil {
		t.Fatal(e)
	}
	if mail.to != "a@example.com" || mail.subject != "Asunto Uno" || !strings.Contains(mail.html, "https://app.example.com/aceptar?token=tok") || store.scrubs != 1 {
		t.Fatal("no se usó la plantilla HTML", mail)
	}
	// Sin URL de aceptación no hay enlace que mostrar: se conserva el texto plano heredado.
	store, mail = &fakeOutbox{current: true}, &htmlMail{}
	if e := NewInvitationMailer(store, mail, "x", "").WithTemplate(tpl).Handle(context.Background(), event()); e != nil || mail.html != "" || mail.fakeMail.calls != 1 {
		t.Fatal("sin URL debe usar texto", e, mail)
	}
}
