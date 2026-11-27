package identityhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	httpmiddleware "github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/identity/domain"
	"github.com/go-chi/chi/v5"
)

type currentProfileGetterStub struct {
	profile  domain.Profile
	err      error
	identity contracts.Identity
}

func (getter *currentProfileGetterStub) Execute(_ context.Context, identity contracts.Identity) (domain.Profile, error) {
	getter.identity = identity
	return getter.profile, getter.err
}

type tokenVerifierStub struct {
	identity contracts.Identity
	err      error
}

func (verifier tokenVerifierStub) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return verifier.identity, verifier.err
}

func TestMeReturnsAuthenticatedProfile(t *testing.T) {
	getter := &currentProfileGetterStub{profile: domain.Profile{
		ID:           "firebase-uid",
		Email:        "usuario@example.com",
		DisplayName:  "Usuario BPM",
		PlatformRole: domain.PlatformRoleUser,
		Status:       domain.UserStatusActive,
		Organizations: []domain.OrganizationMembership{{
			Organization: domain.Organization{ID: "org-1", LegalName: "Restaurante BPM"},
			Role:         domain.MembershipRoleAdmin,
			Locations: []domain.Location{{
				ID:       "location-1",
				Name:     "Local Centro",
				Address:  "Av. Principal 123",
				Timezone: "America/Lima",
			}},
		}},
	}}
	recorder := serveMe(getter, tokenVerifierStub{identity: contracts.Identity{UID: "firebase-uid"}})

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if getter.identity.UID != "firebase-uid" {
		t.Fatalf("expected verified identity, got %+v", getter.identity)
	}
	var body struct {
		Data struct {
			ID            string `json:"id"`
			Organizations []struct {
				ID        string `json:"id"`
				Locations []struct {
					ID string `json:"id"`
				} `json:"locations"`
			} `json:"organizations"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.ID != "firebase-uid" || len(body.Data.Organizations) != 1 || len(body.Data.Organizations[0].Locations) != 1 {
		t.Fatalf("unexpected profile response: %+v", body)
	}
}

func TestMeRejectsUnregisteredUserWithSafeError(t *testing.T) {
	getter := &currentProfileGetterStub{err: domain.ErrUserNotFound}
	recorder := serveMe(getter, tokenVerifierStub{identity: contracts.Identity{UID: "unknown-uid"}})

	assertAPIError(t, recorder, http.StatusForbidden, "USER_NOT_REGISTERED")
}

func TestMeSanitizesInternalErrors(t *testing.T) {
	getter := &currentProfileGetterStub{err: errors.New("password=secret host=database.internal")}
	recorder := serveMe(getter, tokenVerifierStub{identity: contracts.Identity{UID: "firebase-uid"}})

	assertAPIError(t, recorder, http.StatusInternalServerError, response.CodeInternalError)
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "database.internal") {
		t.Fatalf("response leaked internal error: %s", recorder.Body.String())
	}
}

func TestMeRequiresBearerToken(t *testing.T) {
	getter := &currentProfileGetterStub{}
	var logs bytes.Buffer
	handler := NewHandler(getter, slog.New(slog.NewTextHandler(&logs, nil)))
	routes := NewRoutes(handler, httpmiddleware.Authenticate(tokenVerifierStub{}))
	router := chi.NewRouter()
	router.Use(httpmiddleware.RequestID)
	routes.Register(router)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/me", nil))

	assertAPIError(t, recorder, http.StatusUnauthorized, "UNAUTHENTICATED")
}

func serveMe(getter CurrentProfileGetter, verifier tokenVerifierStub) *httptest.ResponseRecorder {
	var logs bytes.Buffer
	handler := NewHandler(getter, slog.New(slog.NewTextHandler(&logs, nil)))
	routes := NewRoutes(handler, httpmiddleware.Authenticate(verifier))
	router := chi.NewRouter()
	router.Use(httpmiddleware.RequestID)
	routes.Register(router)

	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	request.Header.Set("Authorization", "Bearer valid-test-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertAPIError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, recorder.Code, recorder.Body.String())
	}
	var body response.ErrorBody
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Code != code {
		t.Fatalf("unexpected error response: %+v", body)
	}
	if body.RequestID == "" {
		t.Fatal("expected requestId in error response")
	}
}

type onboardingSetterStub struct {
	seen   *bool
	seenAt *time.Time
	err    error
}

func (stub *onboardingSetterStub) Execute(_ context.Context, _ contracts.Identity, seen bool) (*time.Time, error) {
	stub.seen = &seen
	if !seen {
		return nil, stub.err
	}
	return stub.seenAt, stub.err
}

func serveOnboarding(t *testing.T, setter *onboardingSetterStub, method string, token bool) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewHandler(&currentProfileGetterStub{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))).WithOnboarding(setter)
	router := chi.NewRouter()
	router.Use(httpmiddleware.RequestID)
	NewRoutes(handler, httpmiddleware.Authenticate(tokenVerifierStub{identity: contracts.Identity{UID: "uid", EmailVerified: true}})).Register(router)
	request := httptest.NewRequest(method, "/me/onboarding", nil)
	if token {
		request.Header.Set("Authorization", "Bearer valid-test-token")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestOnboardingMarkAndClear(t *testing.T) {
	at := time.Date(2026, 11, 27, 15, 4, 5, 0, time.UTC)
	setter := &onboardingSetterStub{seenAt: &at}
	recorder := serveOnboarding(t, setter, http.MethodPut, true)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"onboardingSeenAt":"2026-11-27T15:04:05Z"`) || setter.seen == nil || !*setter.seen {
		t.Fatalf("unexpected mark response %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = serveOnboarding(t, setter, http.MethodDelete, true)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"onboardingSeenAt":null`) || *setter.seen {
		t.Fatalf("unexpected clear response %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestOnboardingRequiresAuthenticationAndActiveUser(t *testing.T) {
	assertAPIError(t, serveOnboarding(t, &onboardingSetterStub{}, http.MethodPut, false), http.StatusUnauthorized, "UNAUTHENTICATED")
	assertAPIError(t, serveOnboarding(t, &onboardingSetterStub{err: domain.ErrUserNotFound}, http.MethodPut, true), http.StatusForbidden, "USER_NOT_REGISTERED")
}

func TestMeIncludesOnboardingSeenAt(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	recorder := serveMe(&currentProfileGetterStub{profile: domain.Profile{ID: "uid", Status: domain.UserStatusActive, OnboardingSeenAt: &at}}, tokenVerifierStub{identity: contracts.Identity{UID: "uid"}})
	if !strings.Contains(recorder.Body.String(), `"onboardingSeenAt":"2026-11-01T00:00:00Z"`) {
		t.Fatalf("missing onboardingSeenAt: %s", recorder.Body.String())
	}
	recorder = serveMe(&currentProfileGetterStub{profile: domain.Profile{ID: "uid", Status: domain.UserStatusActive}}, tokenVerifierStub{identity: contracts.Identity{UID: "uid"}})
	if !strings.Contains(recorder.Body.String(), `"onboardingSeenAt":null`) {
		t.Fatalf("expected null onboardingSeenAt: %s", recorder.Body.String())
	}
}
