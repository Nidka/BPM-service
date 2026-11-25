package mail

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/config"
	coreresend "github.com/Nidka/BPM-service/internal/core/integrations/resend"
	mailresend "github.com/Nidka/BPM-service/internal/features/mail/adapters/resend"
	"github.com/Nidka/BPM-service/internal/features/mail/application"
	"github.com/go-chi/chi/v5"
)

func pass(next http.Handler) http.Handler { return next }

func post(r http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/email/password-reset", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Sin RESEND_API_KEY (ni Firebase) las rutas existen y piden el respaldo; nada se cae.
func TestRoutesWithoutResendKeyAskClientToFallBack(t *testing.T) {
	r := chi.NewRouter()
	Routes(Dependencies{
		Authenticate: pass,
		Mailer:       NewMailer(config.EmailConfig{From: config.DefaultMailFrom}),
		AppURL:       config.DefaultAppPublicURL,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})(r)
	if w := post(r, `{"email":"a@example.com"}`); w.Code != 503 || !strings.Contains(w.Body.String(), "MAIL_UNAVAILABLE") {
		t.Fatal(w.Code, w.Body.String())
	}
}

type fixedLinks struct{}

func (fixedLinks) VerificationLink(context.Context, string) (string, error) {
	return "https://p.firebaseapp.com/__/auth/action?mode=verifyEmail&oobCode=V", nil
}
func (fixedLinks) PasswordResetLink(_ context.Context, email string) (string, error) {
	if email == "nadie@example.com" {
		return "", application.ErrAccountNotFound
	}
	return "https://p.firebaseapp.com/__/auth/action?mode=resetPassword&oobCode=RESET-CODE&apiKey=k", nil
}

// De punta a punta con un Resend simulado: Firebase genera el enlace, se reescribe y llega a Resend con HTML y texto.
func TestPasswordResetEndToEndWithFakeResend(t *testing.T) {
	var got map[string]any
	var auth string
	resend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth = req.Header.Get("Authorization")
		_ = json.NewDecoder(req.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"id":"re_1"}`))
	}))
	defer resend.Close()
	client := coreresend.New(resend.Client(), "re_secret").WithEndpoint(resend.URL)
	r := chi.NewRouter()
	Routes(Dependencies{
		Authenticate: pass, Links: fixedLinks{}, AppURL: "https://appbpm.com",
		Mailer: mailresend.New(client, "BPM <noreply@appbpm.com>", "ayuda@appbpm.com"),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})(r)

	if w := post(r, `{"email":"Persona@example.com"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if auth != "Bearer re_secret" || got["from"] != "BPM <noreply@appbpm.com>" || got["reply_to"] != "ayuda@appbpm.com" ||
		got["to"].([]any)[0] != "persona@example.com" || got["subject"] != "Crea una nueva contraseña en BPM" {
		t.Fatal("solicitud a Resend", got)
	}
	link := "https://appbpm.com/auth/accion?lang=es&mode=resetPassword&oobCode=RESET-CODE"
	if !strings.Contains(got["text"].(string), link) || !strings.Contains(got["html"].(string), strings.ReplaceAll(link, "&", "&amp;")) {
		t.Fatal("el enlace reescrito no está en HTML y texto")
	}

	// Cuenta inexistente: 202 idéntico y Resend no recibe nada.
	got = nil
	if w := post(r, `{"email":"nadie@example.com"}`); w.Code != 202 || got != nil {
		t.Fatal("cuenta inexistente", w.Code, got)
	}
}

func TestResendFailureTriggersFallback(t *testing.T) {
	resend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer resend.Close()
	r := chi.NewRouter()
	Routes(Dependencies{
		Authenticate: pass, Links: fixedLinks{}, AppURL: "https://appbpm.com",
		Mailer: mailresend.New(coreresend.New(resend.Client(), "k").WithEndpoint(resend.URL), "BPM <noreply@appbpm.com>", ""),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})(r)
	if w := post(r, `{"email":"a@example.com"}`); w.Code != 503 || !strings.Contains(w.Body.String(), "MAIL_UNAVAILABLE") {
		t.Fatal(w.Code, w.Body.String())
	}
}
