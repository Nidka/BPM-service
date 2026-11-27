package platformsettingshttp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/platformsettings/application"
	"github.com/Nidka/BPM-service/internal/features/platformsettings/domain"
	"github.com/go-chi/chi/v5"
)

type verifier struct{}

func (verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "uid", EmailVerified: true}, nil
}

type store struct {
	role    string
	contact domain.SupportContact
}

func (s *store) PlatformRole(context.Context, string) (string, error) { return s.role, nil }
func (s *store) SupportContact(context.Context) (domain.SupportContact, error) {
	return s.contact, nil
}
func (s *store) SaveSupportContact(_ context.Context, _ string, c domain.SupportContact) (domain.SupportContact, error) {
	s.contact = c
	return c, nil
}

func serve(s *store, method, body string, token bool) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	New(application.New(s), nil).Register(r, middleware.Authenticate(verifier{}))
	req := httptest.NewRequest(method, "/platform/support", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token {
		req.Header.Set("Authorization", "Bearer t")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSupportContactEndpoints(t *testing.T) {
	s := &store{role: "USER", contact: domain.SupportContact{Email: domain.DefaultSupportEmail}}
	if rec := serve(s, "GET", "", false); rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	rec := serve(s, "GET", "", true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"supportEmail":"proyectobpmupn@gmail.com"`) || !strings.Contains(rec.Body.String(), `"supportWhatsapp":null`) {
		t.Fatalf("unexpected GET %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(s, "PUT", `{"supportEmail":"soporte@bpm.pe"}`, true); rec.Code != 403 || !strings.Contains(rec.Body.String(), "ACCESS_DENIED") {
		t.Fatalf("non superadmin must get 403, got %d %s", rec.Code, rec.Body.String())
	}
	s.role = "SUPER_ADMIN"
	if rec := serve(s, "PUT", `{"supportEmail":"mal"}`, true); rec.Code != 422 || !strings.Contains(rec.Body.String(), "supportEmail") {
		t.Fatalf("invalid email must get 422, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(s, "PUT", `{"supportEmail":"a@b.pe","extra":1}`, true); rec.Code != 400 {
		t.Fatalf("unknown fields must be rejected, got %d", rec.Code)
	}
	rec = serve(s, "PUT", `{"supportEmail":"soporte@bpm.pe","supportWhatsapp":"+51 987 654 321"}`, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"supportWhatsapp":"51987654321"`) {
		t.Fatalf("unexpected PUT %d %s", rec.Code, rec.Body.String())
	}
}
