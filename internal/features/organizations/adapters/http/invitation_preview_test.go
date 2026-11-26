package organizationhttp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/go-chi/chi/v5"
)

// previewStore resuelve la invitación por el hash del token; guarda los hashes consultados para comprobar que nunca llega el token.
type previewStore struct {
	application.Store
	byHash map[string]domain.InvitationPreview
}

func (s previewStore) InvitationPreviewByHash(_ context.Context, hash string) (domain.InvitationPreview, error) {
	if v, ok := s.byHash[hash]; ok {
		return v, nil
	}
	return domain.InvitationPreview{}, domain.ErrInvitationNotFound
}

type previewWork struct{ store previewStore }

func (w previewWork) Within(_ context.Context, fn func(application.Store) error) error {
	return fn(w.store)
}

const (
	tokenPending = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	tokenExpired = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	tokenRevoked = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	tokenUsed    = "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
	tokenUnknown = "EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE"
)

func previewRouter(limit func(http.Handler) http.Handler, logs io.Writer) http.Handler {
	exp := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	row := func(status string) domain.InvitationPreview {
		return domain.InvitationPreview{OrganizationName: "Restaurante Demo", Role: "WORKER", Email: "nuevo@example.com", Status: status, ExpiresAt: exp}
	}
	store := previewStore{byHash: map[string]domain.InvitationPreview{
		application.HashInvitationToken(tokenPending): row(domain.InvitationPending),
		application.HashInvitationToken(tokenExpired): row(domain.InvitationExpired),
		application.HashInvitationToken(tokenRevoked): row(domain.InvitationRevoked),
		application.HashInvitationToken(tokenUsed):    row(domain.InvitationAccepted),
	}}
	router := chi.NewRouter()
	h := New(application.New(previewWork{store}), slog.New(slog.NewTextHandler(logs, nil)))
	if limit != nil {
		h.WithPreviewLimit(limit)
	}
	// Autenticación que rechazaría cualquier petición: la ruta pública no debe pasar por ella.
	h.Register(router, middleware.Authenticate(verifier{false}))
	return router
}

func previewCall(router http.Handler, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/invitations/preview", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPreviewInvitationPublica(t *testing.T) {
	var logs strings.Builder
	router := previewRouter(nil, &logs)

	rec := previewCall(router, `{"token":"`+tokenPending+`"}`, "application/json")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"organizationName": "Restaurante Demo", "role": "WORKER", "email": "nuevo@example.com", "expiresAt": "2026-12-01T15:00:00Z", "status": "PENDING"}
	if len(out.Data) != len(want) {
		t.Fatalf("campos de más o de menos: %v", out.Data)
	}
	for k, v := range want {
		if out.Data[k] != v {
			t.Errorf("%s = %v, se esperaba %v", k, out.Data[k], v)
		}
	}
	if strings.Contains(rec.Body.String(), tokenPending) {
		t.Fatal("la respuesta no debe repetir el token")
	}

	for _, c := range []struct {
		name, body, code string
		status           int
	}{
		{"vencida", `{"token":"` + tokenExpired + `"}`, "INVITATION_EXPIRED", 409},
		{"revocada", `{"token":"` + tokenRevoked + `"}`, "INVITATION_REVOKED", 409},
		{"usada", `{"token":"` + tokenUsed + `"}`, "INVITATION_ALREADY_USED", 409},
		{"desconocida", `{"token":"` + tokenUnknown + `"}`, "INVITATION_NOT_FOUND", 404},
		{"mal formada", `{"token":"x"}`, "INVITATION_NOT_FOUND", 404},
		{"vacía", `{"token":""}`, "INVITATION_NOT_FOUND", 404},
		{"campo extra", `{"token":"` + tokenPending + `","x":1}`, "INVALID_REQUEST", 400},
		{"sin cuerpo JSON", `x`, "INVALID_REQUEST", 400},
		{"cuerpo grande", `{"token":"` + strings.Repeat("A", 2000) + `"}`, "INVALID_REQUEST", 400},
	} {
		rec := previewCall(router, c.body, "application/json")
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.code) || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v %s", c.name, rec.Code, rec.Header(), rec.Body.String())
		}
		for _, leak := range []string{"nuevo@example.com", "Restaurante Demo"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: un error no debe revelar datos de la invitación (%s)", c.name, leak)
			}
		}
	}
	if rec := previewCall(router, `{"token":"`+tokenPending+`"}`, ""); rec.Code != 415 {
		t.Errorf("sin Content-Type JSON: %d", rec.Code)
	}
	if strings.Contains(logs.String(), tokenPending) || strings.Contains(logs.String(), tokenUnknown) {
		t.Fatal("el token no debe aparecer en los registros")
	}
}

func TestPreviewInvitationLimitaPorIP(t *testing.T) {
	limiter := middleware.NewIPRateLimiter(0.001, 2, false)
	router := previewRouter(limiter.Middleware, io.Discard)
	statuses := []int{}
	for range 3 {
		statuses = append(statuses, previewCall(router, `{"token":"`+tokenUnknown+`"}`, "application/json").Code)
	}
	if statuses[0] != 404 || statuses[1] != 404 || statuses[2] != 429 {
		t.Fatalf("la tercera consulta seguida debe limitarse: %v", statuses)
	}
}
