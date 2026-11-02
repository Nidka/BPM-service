package checklisthttp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/checklists/application"
	"github.com/go-chi/chi/v5"
)

type verifier struct{ verified bool }

func (v verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "user", EmailVerified: v.verified}, nil
}

type brokenWork struct{}

func (brokenWork) Within(context.Context, func(application.Store) error) error {
	return errors.New("postgres://private:secret@internal/db")
}

func (brokenWork) WithinAssignments(context.Context, func(application.AssignmentStore) error) error {
	return errors.New("private storage error")
}

func TestProtectedChecklistRoutes(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, token, code string
		verified, idempotency                 bool
		status                                int
	}{
		{"assignment missing token", "GET", "/organizations/org/checklist-assignments", "", "", "UNAUTHENTICATED", true, false, 401},
		{"assignment invalid query", "GET", "/organizations/org/checklist-assignments?role=ADMIN", "", "token", "INVALID_PAGINATION", true, false, 400},
		{"assignment unknown field", "POST", "/organizations/org/checklist-assignments", `{"actor":"admin"}`, "token", "INVALID_REQUEST", true, false, 400},
		{"assignment invalid body", "POST", "/organizations/org/checklist-assignments", `{}`, "token", "VALIDATION_ERROR", true, false, 422},
		{"assignment storage unavailable", "GET", "/organizations/org/checklist-assignments", "", "token", "INTERNAL_ERROR", true, false, 500},
		{"missing token", "GET", "/organizations/org/checklists", "", "", "UNAUTHENTICATED", true, false, 401},
		{"history missing token", "GET", "/organizations/org/checklist-runs", "", "", "UNAUTHENTICATED", true, false, 401},
		{"history repeated scope", "GET", "/organizations/org/checklist-runs?scope=mine&scope=team", "", "token", "INVALID_PAGINATION", true, false, 400},
		{"history unknown scope", "GET", "/organizations/org/checklist-runs?scope=all", "", "token", "INVALID_PAGINATION", true, false, 400},
		{"history invalid status", "GET", "/organizations/org/checklist-runs?status=OPEN", "", "token", "INVALID_PAGINATION", true, false, 400},
		{"history unknown parameter", "GET", "/organizations/org/checklist-runs?actor=other", "", "token", "INVALID_PAGINATION", true, false, 400},
		{"unverified", "GET", "/organizations/org/checklists", "", "token", "EMAIL_NOT_VERIFIED", false, false, 403},
		{"internal error", "GET", "/organizations/org/checklists", "", "token", "INTERNAL_ERROR", true, false, 500},
		{"unknown field", "POST", "/organizations/org/checklists", `{"platformRole":"SUPER_ADMIN"}`, "token", "INVALID_REQUEST", true, false, 400},
		{"invalid template", "POST", "/organizations/org/checklists", `{}`, "token", "VALIDATION_ERROR", true, false, 422},
		{"invalid run", "POST", "/organizations/org/checklist-runs", `{}`, "token", "VALIDATION_ERROR", true, false, 422},
		{"missing idempotency", "PUT", "/organizations/org/checklist-runs/10000000-0000-0000-0000-000000000001/answers", `{"rowVersion":1,"answers":[]}`, "token", "VALIDATION_ERROR", true, false, 422},
		{"invalid submit", "POST", "/organizations/org/checklist-runs/10000000-0000-0000-0000-000000000001/submit", `{"rowVersion":0}`, "token", "VALIDATION_ERROR", true, true, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			router := chi.NewRouter()
			New(application.New(brokenWork{}), application.NewAssignments(brokenWork{}), logger).Register(router, middleware.Authenticate(verifier{tc.verified}))
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.idempotency {
				request.Header.Set("Idempotency-Key", "operation-001")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) {
				t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("leaked infrastructure error")
			}
		})
	}
}

func TestLegacyReceiptReturnsEvidenceCountsObject(t *testing.T) {
	body, err := json.Marshal(run(domain.Run{}))
	if err != nil || !strings.Contains(string(body), `"evidenceCounts":{}`) {
		t.Fatal("reintento anterior a multimedia debe mantener objeto JSON", err, string(body))
	}
}
