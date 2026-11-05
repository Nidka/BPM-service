package mediahttp

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/media/application"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/go-chi/chi/v5"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type verifier struct{ verified bool }

func (v verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "chef", EmailVerified: v.verified}, nil
}

type brokenWork struct{}

func (brokenWork) Within(context.Context, func(application.Store) error) error {
	return errors.New("private postgres credentials")
}
func TestEvidenceHTTPRequiresIdentityStrictJSONAndSanitizesFailures(t *testing.T) {
	base := "/organizations/org/media/10000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		method, path, body, token, code string
		verified                        bool
		status                          int
	}{
		{"POST", base + "/confirm", "{}", "", "UNAUTHENTICATED", true, 401},
		{"POST", base + "/confirm", "{}", "token", "EMAIL_NOT_VERIFIED", false, 403},
		{"POST", base + "/confirm", `{"role":"ADMIN"}`, "token", "INVALID_REQUEST", true, 400},
		{"POST", "/organizations/org/media/uploads", "{}", "token", "VALIDATION_ERROR", true, 422},
		{"POST", base + "/download", "{}", "token", "INTERNAL_ERROR", true, 500},
		{"GET", "/organizations/org/checklist-runs/10000000-0000-0000-0000-000000000001/items/20000000-0000-0000-0000-000000000001/evidence?actor=other", "", "token", "INVALID_REQUEST", true, 400},
	} {
		router := chi.NewRouter()
		New(application.New(brokenWork{}, nil, domain.Policy{MaxBytes: 1024, MaxFiles: 3, AllowedMIMEs: []string{"image/png"}}), slog.New(slog.NewTextHandler(io.Discard, nil))).Register(router, middleware.Authenticate(verifier{tc.verified}))
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != tc.status || !strings.Contains(out.Body.String(), tc.code) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, out.Code, out.Body.String())
		}
		if strings.Contains(out.Body.String(), "credentials") {
			t.Fatal("filtró error interno")
		}
	}
}
