package mailhttp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	m "github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/mail/application"
	"github.com/go-chi/chi/v5"
)

type senderFake struct {
	verifyTo, resetTo []string
	err               error
}

func (s *senderFake) SendVerification(_ context.Context, email string) error {
	s.verifyTo = append(s.verifyTo, email)
	return s.err
}
func (s *senderFake) SendPasswordReset(_ context.Context, email string) error {
	s.resetTo = append(s.resetTo, email)
	return s.err
}

type verifier struct{ identity contracts.Identity }

func (v verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return v.identity, nil
}

type allowN struct{ left int }

func (a *allowN) Allow(string) bool { a.left--; return a.left >= 0 }

type fixture struct {
	router chi.Router
	sender *senderFake
	logs   *bytes.Buffer
}

func newFixture(identity contracts.Identity, resetAllowed int) fixture {
	sender, logs := &senderFake{}, &bytes.Buffer{}
	pass := func(next http.Handler) http.Handler { return next }
	h := New(sender, m.Authenticate(verifier{identity}), pass, &allowN{left: 2}, &allowN{left: resetAllowed}, slog.New(slog.NewTextHandler(logs, nil)))
	r := chi.NewRouter()
	r.Use(m.RequestID)
	h.Register(r)
	return fixture{r, sender, logs}
}

func do(f fixture, path, body string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer token")
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

var unverified = contracts.Identity{UID: "uid-1", Email: "Persona@Example.com"}

func TestVerificationRequiresAuthAndUsesTokenEmailOnly(t *testing.T) {
	f := newFixture(unverified, 5)
	if w := do(f, "/auth/email/verification", "", false); w.Code != 401 {
		t.Fatal("sin token debe ser 401", w.Code)
	}
	// Un cuerpo con otro correo se ignora: el destinatario sale del token.
	w := do(f, "/auth/email/verification", `{"email":"victima@example.com"}`, true)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"SENT"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(f.sender.verifyTo) != 1 || f.sender.verifyTo[0] != "Persona@Example.com" {
		t.Fatal("destinatario", f.sender.verifyTo)
	}
}

func TestVerificationAlreadyVerifiedDoesNotSend(t *testing.T) {
	f := newFixture(contracts.Identity{UID: "u", Email: "a@b.pe", EmailVerified: true}, 5)
	w := do(f, "/auth/email/verification", "", true)
	if w.Code != 202 || !strings.Contains(w.Body.String(), "ALREADY_VERIFIED") || len(f.sender.verifyTo) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestVerificationRateLimitedPerAccount(t *testing.T) {
	f := newFixture(unverified, 5)
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, do(f, "/auth/email/verification", "", true).Code)
	}
	if codes[0] != 202 || codes[1] != 202 || codes[2] != 429 {
		t.Fatal(codes)
	}
}

func TestPasswordResetAlwaysAcceptedAndPublic(t *testing.T) {
	f := newFixture(contracts.Identity{}, 5)
	w := do(f, "/auth/email/password-reset", `{"email":" Persona@Example.com "}`, false)
	if w.Code != 202 || !strings.Contains(w.Body.String(), "ACCEPTED") || len(f.sender.resetTo) != 1 || f.sender.resetTo[0] != "persona@example.com" {
		t.Fatal(w.Code, w.Body.String(), f.sender.resetTo)
	}
}

func TestPasswordResetValidation(t *testing.T) {
	f := newFixture(contracts.Identity{}, 5)
	for _, body := range []string{`{}`, `{"email":"no-es-correo"}`, `{"email":"a@b.pe\nBcc: x@y.pe"}`} {
		if w := do(f, "/auth/email/password-reset", body, false); w.Code != 422 || !strings.Contains(w.Body.String(), "VALIDATION_ERROR") {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"email":"a@b.pe","admin":true}`, `{"email":"a@b.pe"} {}`, `[`, ``} {
		if w := do(f, "/auth/email/password-reset", body, false); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	if len(f.sender.resetTo) != 0 {
		t.Fatal("una solicitud inválida llegó al caso de uso")
	}
}

func TestPasswordResetRateLimitPerEmail(t *testing.T) {
	f := newFixture(contracts.Identity{}, 1)
	if w := do(f, "/auth/email/password-reset", `{"email":"a@b.pe"}`, false); w.Code != 202 {
		t.Fatal(w.Code)
	}
	w := do(f, "/auth/email/password-reset", `{"email":"a@b.pe"}`, false)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "RATE_LIMITED") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestFallbackErrorIsSpecificAndLogsNothingSensitive(t *testing.T) {
	f := newFixture(unverified, 5)
	f.sender.err = application.ErrUnavailable
	for _, tc := range []struct{ path, body string }{
		{"/auth/email/password-reset", `{"email":"secreto@example.com"}`},
		{"/auth/email/verification", ``},
	} {
		w := do(f, tc.path, tc.body, true)
		if w.Code != 503 || !strings.Contains(w.Body.String(), CodeMailUnavailable) {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	logged := f.logs.String()
	if !strings.Contains(logged, CodeMailUnavailable) {
		t.Fatal("debe registrarse el código", logged)
	}
	for _, secret := range []string{"secreto@example.com", "example.com", "oobCode", "Persona"} {
		if strings.Contains(logged, secret) {
			t.Fatal("el log contiene datos sensibles:", secret)
		}
	}
}

func TestUnexpectedErrorsAlsoUseTheFallback(t *testing.T) {
	f := newFixture(contracts.Identity{}, 5)
	f.sender.err = errors.New("firebase dijo a@b.pe")
	w := do(f, "/auth/email/password-reset", `{"email":"a@b.pe"}`, false)
	if w.Code != 503 || strings.Contains(w.Body.String(), "firebase dijo") {
		t.Fatal(w.Code, w.Body.String())
	}
}
