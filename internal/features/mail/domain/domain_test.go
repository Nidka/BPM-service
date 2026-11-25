package domain

import (
	"errors"
	"strings"
	"testing"
)

const firebaseLink = "https://app-bpm-af59c.firebaseapp.com/__/auth/action?apiKey=AIzaSyFAKE&mode=resetPassword&oobCode=AbC_d-123%3D&continueUrl=https%3A%2F%2Fevil.example&lang=en"

func TestActionLinkRewritesToAppPageKeepingOnlyModeCodeAndLang(t *testing.T) {
	got, err := ActionLink(firebaseLink, "https://appbpm.com/", ModeResetPassword)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://appbpm.com/auth/accion?lang=es&mode=resetPassword&oobCode=AbC_d-123%3D"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	for _, leaked := range []string{"apiKey", "firebaseapp", "continueUrl", "evil"} {
		if strings.Contains(got, leaked) {
			t.Fatal("el enlace conserva", leaked)
		}
	}
}

func TestActionLinkRejectsWrongModeBadCodeAndBadBase(t *testing.T) {
	cases := []struct{ link, base, mode string }{
		{firebaseLink, "https://appbpm.com", ModeVerifyEmail},
		{"https://x.example/__/auth/action?mode=verifyEmail", "https://appbpm.com", ModeVerifyEmail},
		{"https://x.example/a?mode=verifyEmail&oobCode=has%20space", "https://appbpm.com", ModeVerifyEmail},
		{"https://x.example/a?mode=verifyEmail&oobCode=ok", "javascript:alert(1)", ModeVerifyEmail},
		{"https://x.example/a?mode=verifyEmail&oobCode=ok", "", ModeVerifyEmail},
		{"%zz", "https://appbpm.com", ModeVerifyEmail},
	}
	for _, c := range cases {
		if _, err := ActionLink(c.link, c.base, c.mode); !errors.Is(err, ErrInvalidActionLink) {
			t.Fatalf("aceptó %+v", c)
		}
	}
}

func TestVerifyEmailTemplate(t *testing.T) {
	link := "https://appbpm.com/auth/accion?lang=es&mode=verifyEmail&oobCode=abc"
	m := VerifyEmail(ActionData{AppURL: "https://appbpm.com", Link: link})
	if m.Subject != "Confirma tu correo en BPM" {
		t.Fatal(m.Subject)
	}
	for _, want := range []string{
		`lang="es"`, "Confirma tu correo", "Verificar mi correo", "#005B59", "Baloo 2", "Nunito", "prefers-color-scheme:dark",
		`name="color-scheme"`, `https://appbpm.com/composeResources/pe.bpm.resources/drawable/mascot_ready.png`,
		`href="https://appbpm.com/auth/accion?lang=es&amp;mode=verifyEmail&amp;oobCode=abc"`, `role="presentation"`,
	} {
		if !strings.Contains(m.HTML, want) {
			t.Fatalf("el HTML no contiene %q", want)
		}
	}
	if strings.Contains(m.HTML, "<script") {
		t.Fatal("no debe haber scripts")
	}
	if !strings.Contains(m.Text, link) || !strings.Contains(m.Text, "Verificar mi correo") || strings.Contains(m.Text, "<") {
		t.Fatal("texto alternativo incorrecto", m.Text)
	}
}

func TestPasswordResetTemplate(t *testing.T) {
	m := PasswordReset(ActionData{AppURL: "https://appbpm.com", Link: "https://appbpm.com/auth/accion?mode=resetPassword&oobCode=x"})
	if m.Subject != "Crea una nueva contraseña en BPM" || !strings.Contains(m.HTML, "Crear nueva contraseña") || !strings.Contains(m.HTML, "mascot_fix.png") {
		t.Fatal(m.Subject)
	}
}

func TestInvitationTemplateEscapesAndFormatsDate(t *testing.T) {
	m := Invitation(InvitationData{AppURL: "https://appbpm.com", Link: "https://appbpm.com/cuenta/invitacion?token=t", OrganizationName: `Café <b>"Sol"</b> & Co`, Email: "a@example.com", ExpiresAt: "2026-01-02T03:00:00Z"})
	if strings.Contains(m.HTML, "<b>") || !strings.Contains(m.HTML, "&lt;b&gt;") {
		t.Fatal("el nombre de la organización no se escapó")
	}
	// 03:00 UTC es 22:00 del día 1 en Lima.
	if !strings.Contains(m.HTML, "1 de enero de 2026") || !strings.Contains(m.Text, "1 de enero de 2026") {
		t.Fatal("fecha en español", m.Text)
	}
	if !strings.Contains(m.Subject, "Invitación a Café") || !strings.Contains(m.HTML, "Aceptar invitación") || !strings.Contains(m.HTML, "a@example.com") {
		t.Fatal(m.Subject)
	}
}

func TestTemplatesNeverEmitUnsafeLinks(t *testing.T) {
	m := VerifyEmail(ActionData{AppURL: "javascript:alert(1)", Link: "javascript:alert(1)"})
	if strings.Contains(m.HTML, "javascript:") || !strings.Contains(m.HTML, `href="#"`) || strings.Contains(m.HTML, "mascot") {
		t.Fatal("esquema no permitido")
	}
}

func TestInvitationWithoutExpiryOrName(t *testing.T) {
	m := Invitation(InvitationData{AppURL: "https://appbpm.com", Link: "https://appbpm.com/x", Email: "a@b.pe", ExpiresAt: "no-fecha"})
	if !strings.Contains(m.Subject, "una organización") || strings.Contains(m.Text, "vence el") {
		t.Fatal(m.Subject, m.Text)
	}
}
