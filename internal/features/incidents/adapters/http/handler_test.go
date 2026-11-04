package incidentshttp

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/incidents/application"
	"github.com/go-chi/chi/v5"
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
func TestClosedInputsAndSanitizedFailures(t *testing.T) {
	r := chi.NewRouter()
	New(application.New(unavailable{}), nil).Register(r, middleware.Authenticate(verifier{}))
	base := "/organizations/test"
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", base + "/incidents", "", 400},
		{"GET", base + "/incident-catalog?role=ADMIN", "", 400},
		{"GET", base + "/incident-catalog", "extra", 400},
		{"POST", base + "/incident-catalog", `{"id":"10000000-0000-0000-0000-000000000001","code":"A","name":"Nombre","area":"Área","normativeCriterion":"Criterio","riskLevel":"HIGH"}`, 422},
		{"POST", base + "/incidents", `{"actor":"other"}`, 400},
		{"POST", base + "/incidents", `{"id":"a","id":"b"}`, 400},
		{"POST", base + "/incidents", string([]byte{'{', '"', 'i', 'd', '"', ':', '"', 0xff, '"', '}'}), 400},
		{"POST", base + "/incidents/10000000-0000-0000-0000-000000000001/actions", `{"operationId":"20000000-0000-0000-0000-000000000001","rowVersion":1,"actionType":"START","note":""}`, 422},
		{"GET", base + "/incident-catalog", "", 500},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer test")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "test-stable-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s %s got %d want %d: %s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private SQL") || strings.Contains(w.Body.String(), "password") {
			t.Fatal("filtró interno")
		}
	}
}
