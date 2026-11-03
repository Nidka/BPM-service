package operationshttp

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/go-chi/chi/v5"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type verifier struct{}

func (verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "chef", EmailVerified: true}, nil
}

type unavailable struct{}

func (unavailable) Within(context.Context, func(application.Store) error) error {
	return errors.New("private SQL password")
}
func TestClosedContractAndSafeFailures(t *testing.T) {
	router := chi.NewRouter()
	New(application.New(unavailable{}), slog.New(slog.NewTextHandler(io.Discard, nil))).Register(router, middleware.Authenticate(verifier{}))
	base := "/organizations/10000000-0000-0000-0000-000000000001"
	for _, tc := range []struct {
		method, path, body, token string
		want                      int
	}{
		{"GET", base + "/products", "", "", 401},
		{"GET", base + "/storage-lots", "", "token", 400},
		{"GET", base + "/products?role=ADMIN", "", "token", 400},
		{"POST", base + "/suppliers", `{"id":"10000000-0000-0000-0000-000000000001","name":"Proveedor","actor":"other"}`, "token", 400},
		{"POST", base + "/suppliers", `{"name":"1"}`, "token", 422},
		{"GET", base + "/products", "", "token", 500},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "stable-test-key")
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Fatalf("%s %s want%d got%d %s", tc.method, tc.path, tc.want, res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "private SQL") || strings.Contains(res.Body.String(), "password") {
			t.Fatal("filtró detalle interno")
		}
	}
	_ = http.MethodGet
}
