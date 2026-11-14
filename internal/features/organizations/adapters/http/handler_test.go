package organizationhttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/go-chi/chi/v5"
)

type verifier struct{ verified bool }

func (v verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "test", EmailVerified: v.verified}, nil
}

type brokenWork struct{}

func (brokenWork) Within(context.Context, func(application.Store) error) error {
	return errors.New("postgres://private:secret@internal/db")
}

func TestProtectedOrganizationRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, token string
		verified                        bool
		status                          int
		code                            string
	}{
		{"missing token", "GET", "/organizations", "", "", true, 401, "UNAUTHENTICATED"},
		{"unverified", "GET", "/organizations", "", "token", false, 403, "EMAIL_NOT_VERIFIED"},
		{"internal", "GET", "/organizations", "", "token", true, 500, "INTERNAL_ERROR"},
		{"invalid cursor", "GET", "/organizations?pageToken=bad", "", "token", true, 400, "INVALID_PAGINATION"},
		{"duplicate page size", "GET", "/organizations?pageSize=1&pageSize=2", "", "token", true, 400, "INVALID_PAGINATION"},
		{"invalid page limit", "GET", "/organizations/org/locations?pageSize=101", "", "token", true, 400, "INVALID_PAGINATION"},
		{"unknown member filter", "GET", "/organizations/org/members?role=SUPER_ADMIN", "", "token", true, 400, "INVALID_PAGINATION"},
		{"workspace rejects list cursor", "GET", "/organizations/org/workspace?pageToken=bad", "", "token", true, 400, "INVALID_PAGINATION"},
		{"unknown field", "POST", "/organizations", `{"platformRole":"SUPER_ADMIN"}`, "token", true, 400, "INVALID_REQUEST"},
		{"empty form", "POST", "/organizations", `{}`, "token", true, 422, "VALIDATION_ERROR"},
		{"invalid location", "POST", "/organizations/org/locations", `{}`, "token", true, 422, "VALIDATION_ERROR"},
		{"invalid member", "POST", "/organizations/org/members", `{"email":"bad","role":"SUPER_ADMIN"}`, "token", true, 422, "VALIDATION_ERROR"},
		{"member check requires authentication", "POST", "/organizations/org/members/check", `{"email":"test@example.com"}`, "", true, 401, "UNAUTHENTICATED"},
		{"member check requires verified email", "POST", "/organizations/org/members/check", `{"email":"test@example.com"}`, "token", false, 403, "EMAIL_NOT_VERIFIED"},
		{"member check rejects invalid email", "POST", "/organizations/org/members/check", `{"email":"bad"}`, "token", true, 422, "VALIDATION_ERROR"},
		{"member check rejects role", "POST", "/organizations/org/members/check", `{"email":"test@example.com","role":"SUPER_ADMIN"}`, "token", true, 400, "INVALID_REQUEST"},
		{"member search requires authentication", "POST", "/organizations/org/members/search", `{"query":"chef"}`, "", true, 401, "UNAUTHENTICATED"},
		{"member search requires verified email", "POST", "/organizations/org/members/search", `{"query":"chef"}`, "token", false, 403, "EMAIL_NOT_VERIFIED"},
		{"member search rejects short prefix", "POST", "/organizations/org/members/search", `{"query":"ch"}`, "token", true, 422, "VALIDATION_ERROR"},
		{"member search rejects role", "POST", "/organizations/org/members/search", `{"query":"chef","role":"ADMIN"}`, "token", true, 400, "INVALID_REQUEST"},
		{"member search rejects URL query", "POST", "/organizations/org/members/search?query=chef", `{"query":"chef"}`, "token", true, 400, "INVALID_REQUEST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			router := chi.NewRouter()
			New(application.New(brokenWork{}), logger).Register(router, middleware.Authenticate(verifier{tc.verified}))
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatalf("unexpected response %d %s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("leaked internal error")
			}
		})
	}
}
