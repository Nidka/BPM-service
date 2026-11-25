package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/mail/domain"
)

type linksFake struct {
	verify, reset string
	err           error
	asked         []string
}

func (f *linksFake) VerificationLink(_ context.Context, email string) (string, error) {
	f.asked = append(f.asked, email)
	return f.verify, f.err
}
func (f *linksFake) PasswordResetLink(_ context.Context, email string) (string, error) {
	f.asked = append(f.asked, email)
	return f.reset, f.err
}

type mailerFake struct {
	configured bool
	err        error
	sent       []domain.Message
	to         []string
}

func (m *mailerFake) Configured() bool { return m.configured }
func (m *mailerFake) Send(_ context.Context, to string, msg domain.Message) error {
	if m.err != nil {
		return m.err
	}
	m.to = append(m.to, to)
	m.sent = append(m.sent, msg)
	return nil
}

const (
	verifyLink = "https://p.firebaseapp.com/__/auth/action?mode=verifyEmail&oobCode=CODE1&apiKey=k"
	resetLink  = "https://p.firebaseapp.com/__/auth/action?mode=resetPassword&oobCode=CODE2&apiKey=k"
)

func TestSendVerificationRewritesLinkAndSendsToGivenEmail(t *testing.T) {
	links, mailer := &linksFake{verify: verifyLink}, &mailerFake{configured: true}
	if err := New(links, mailer, "https://appbpm.com").SendVerification(context.Background(), " Persona@Example.com "); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 || mailer.to[0] != "persona@example.com" {
		t.Fatal("destinatario", mailer.to)
	}
	if !strings.Contains(mailer.sent[0].HTML, "auth/accion?lang=es&amp;mode=verifyEmail&amp;oobCode=CODE1") || strings.Contains(mailer.sent[0].HTML, "firebaseapp") {
		t.Fatal("el enlace no se reescribió")
	}
}

func TestSendPasswordResetUnknownAccountSendsNothingAndSucceeds(t *testing.T) {
	links, mailer := &linksFake{err: ErrAccountNotFound}, &mailerFake{configured: true}
	if err := New(links, mailer, "https://appbpm.com").SendPasswordReset(context.Background(), "nadie@example.com"); err != nil || len(mailer.sent) != 0 {
		t.Fatal("una cuenta inexistente debe ser indistinguible", err, len(mailer.sent))
	}
}

func TestFallbackWhenProviderMissingOrFails(t *testing.T) {
	ctx := context.Background()
	// Sin RESEND_API_KEY: ni siquiera se pide el enlace a Firebase.
	links := &linksFake{reset: resetLink}
	if err := New(links, &mailerFake{}, "https://appbpm.com").SendPasswordReset(ctx, "a@example.com"); !errors.Is(err, ErrUnavailable) || len(links.asked) != 0 {
		t.Fatal("sin proveedor debe responder ErrUnavailable sin consultar a Firebase", err)
	}
	// Resend falla.
	if err := New(links, &mailerFake{configured: true, err: errors.New("boom a@example.com")}, "https://appbpm.com").SendPasswordReset(ctx, "a@example.com"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "example.com") {
		t.Fatal(err)
	}
	// Firebase Admin falla o devuelve un enlace irreconocible.
	if err := New(&linksFake{err: errors.New("x")}, &mailerFake{configured: true}, "https://appbpm.com").SendVerification(ctx, "a@example.com"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := New(&linksFake{verify: "https://x.example/?mode=verifyEmail"}, &mailerFake{configured: true}, "https://appbpm.com").SendVerification(ctx, "a@example.com"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	// Sin generador de enlaces (Firebase deshabilitado).
	if err := New(nil, &mailerFake{configured: true}, "https://appbpm.com").SendVerification(ctx, "a@example.com"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestInvalidEmails(t *testing.T) {
	s := New(&linksFake{}, &mailerFake{configured: true}, "https://appbpm.com")
	for _, bad := range []string{"", "x", "a@b.pe\nBcc: z@z.pe", "Nombre <a@b.pe>", "a@b.pe, c@d.pe", strings.Repeat("a", 260) + "@b.pe"} {
		if err := s.SendPasswordReset(context.Background(), bad); !errors.Is(err, ErrInvalidEmail) {
			t.Fatalf("aceptó %q", bad)
		}
	}
}

func TestKeyedLimiterSlidingWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewKeyedLimiter(2, time.Minute, func() time.Time { return now })
	if !l.Allow("a") || !l.Allow("a") || l.Allow("a") {
		t.Fatal("límite por clave")
	}
	if !l.Allow("b") {
		t.Fatal("otra clave no se ve afectada")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("a") {
		t.Fatal("la ventana debe liberarse")
	}
}
