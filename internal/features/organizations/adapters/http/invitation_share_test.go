package organizationhttp

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

type shareStore struct{ application.Store }

func (shareStore) Actor(context.Context, string) (domain.Actor, error) {
	return domain.Actor{ID: "admin"}, nil
}
func (shareStore) LockForManagement(context.Context, domain.Actor, string) (string, error) {
	return "ADMIN", nil
}
func (shareStore) Role(context.Context, domain.Actor, string) (string, error) { return "ADMIN", nil }
func (shareStore) CheckLocations(context.Context, string, []string) error     { return nil }
func (shareStore) CreateInvitation(_ context.Context, _ domain.Actor, _ string, v domain.Invitation) (domain.Invitation, bool, error) {
	v.Status, v.ExpiresAt = domain.InvitationPending, time.Now().Add(time.Hour)
	return v, true, nil
}
func (shareStore) QueueInvitationEmail(context.Context, domain.Actor, domain.Invitation, string) error {
	return nil
}
func (shareStore) LockInvitation(_ context.Context, _, id string) (domain.Invitation, error) {
	return domain.Invitation{ID: id, Status: domain.InvitationPending}, nil
}
func (shareStore) ResendInvitation(_ context.Context, _ domain.Actor, _, id, hash string) (domain.Invitation, error) {
	return domain.Invitation{ID: id, Email: "a@example.com", Role: "ADMIN", Status: domain.InvitationPending, TokenHash: hash}, nil
}
func (shareStore) InvitationsPage(context.Context, string, string, pagination.Window) ([]domain.Invitation, error) {
	return []domain.Invitation{{ID: "30000000-0000-0000-0000-000000000001", Email: "a@example.com", Role: "ADMIN", Status: domain.InvitationPending, TokenHash: "hash"}}, nil
}

type shareWork struct{}

func (shareWork) Within(_ context.Context, fn func(application.Store) error) error {
	return fn(shareStore{})
}

func callShare(t *testing.T, acceptURL, method, path, body string) string {
	t.Helper()
	router := chi.NewRouter()
	svc := application.New(shareWork{}, application.WithInvitationDelivery(acceptURL, false))
	New(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(router, middleware.Authenticate(verifier{true}))
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 201 && rec.Code != 200 {
		t.Fatalf("estado %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

const (
	createBody = `{"id":"30000000-0000-0000-0000-000000000001","email":"a@example.com","role":"ADMIN"}`
	resendPath = "/organizations/org/invitations/30000000-0000-0000-0000-000000000001/resend"
)

func TestInvitationResponsesExposeAcceptURLOnlyOnCreateAndResend(t *testing.T) {
	const url = "https://appbpm.com/cuenta/invitacion"
	for name, out := range map[string]string{
		"crear":    callShare(t, url, "POST", "/organizations/org/invitations", createBody),
		"reenviar": callShare(t, url, "POST", resendPath, ""),
	} {
		if !strings.Contains(out, `"acceptUrl":"`+url+`?token=`) || strings.Contains(out, `"token":"`) || strings.Contains(out, "tokenHash") {
			t.Fatal(name, out)
		}
	}
	if list := callShare(t, url, "GET", "/organizations/org/invitations", ""); strings.Contains(list, "acceptUrl") || strings.Contains(list, "token") {
		t.Fatal("la lista no debe exponer el enlace", list)
	}
}

func TestInvitationResponsesWithoutURLReturnNullAndToken(t *testing.T) {
	for name, out := range map[string]string{
		"crear":    callShare(t, "", "POST", "/organizations/org/invitations", createBody),
		"reenviar": callShare(t, "", "POST", resendPath, ""),
	} {
		if !strings.Contains(out, `"acceptUrl":null`) || !strings.Contains(out, `"token":"`) {
			t.Fatal(name, out)
		}
	}
	if list := callShare(t, "", "GET", "/organizations/org/invitations", ""); strings.Contains(list, "acceptUrl") || strings.Contains(list, `"token"`) {
		t.Fatal(list)
	}
}
