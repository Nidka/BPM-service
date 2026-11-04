package notificationshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const deviceTestID = "a0000000-0000-4000-8000-000000000001"
const deviceTestBody = `{"id":"A0000000-0000-4000-8000-000000000001","installationId":"b0000000-0000-4000-8000-000000000002","platform":"ANDROID","pushToken":"fixture-private-token"}`

type verifiedDeviceUser struct{ verified bool }

func (v verifiedDeviceUser) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "usuario_😀", EmailVerified: v.verified}, nil
}

type deviceHTTPStore struct {
	calls     int
	user, org string
	input     domain.DeviceInput
	fail      error
}

func (s *deviceHTTPStore) Within(ctx context.Context, fn func(application.DeviceStore) error) error {
	s.calls++
	if s.fail != nil {
		return s.fail
	}
	return fn(s)
}
func (s *deviceHTTPStore) Authorize(_ context.Context, user, org string) error {
	s.user = user
	s.org = org
	return nil
}
func (s *deviceHTTPStore) Register(_ context.Context, _, _ string, input domain.DeviceInput) (domain.Device, error) {
	s.input = input
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	return domain.Device{ID: input.ID, Platform: input.Platform, Status: "ACTIVE", LastSeenAt: &now}, nil
}
func (s *deviceHTTPStore) Deactivate(_ context.Context, _, _, id string) (domain.Device, error) {
	return domain.Device{ID: id, Platform: "ANDROID", Status: "INACTIVE"}, nil
}
func deviceRouter(s *deviceHTTPStore, verified bool, logs *bytes.Buffer) http.Handler {
	router := chi.NewRouter()
	New(application.NewDevices(s), slog.New(slog.NewTextHandler(logs, nil))).Register(router, middleware.Authenticate(verifiedDeviceUser{verified}))
	return router
}
func TestDevicesPrivateProjectionAndVerifiedOwner(t *testing.T) {
	s := &deviceHTTPStore{}
	logs := &bytes.Buffer{}
	router := deviceRouter(s, true, logs)
	req := httptest.NewRequest("POST", "/organizations/org_1/notifications/devices", strings.NewReader(deviceTestBody))
	req.Header.Set("Authorization", "Bearer fixture")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal("status", res.Code)
	}
	if s.user != "usuario_😀" || s.org != "org_1" || s.input.ID != deviceTestID {
		t.Fatal("owner or UUID changed")
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(res.Body.Bytes(), &payload) != nil || len(payload.Data) != 4 || payload.Data["id"] != deviceTestID || payload.Data["platform"] != "ANDROID" || payload.Data["status"] != "ACTIVE" {
		t.Fatal("public device contract invalid")
	}
	for _, private := range []string{"pushToken", "installationId", "fixture-private-token", "usuario_"} {
		if strings.Contains(res.Body.String(), private) || strings.Contains(logs.String(), private) {
			t.Fatal("private field exposed")
		}
	}
}
func TestDevicesRejectClosedInputBeforeRepository(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, token string
		verified                bool
		want                    int
	}{
		{"auth", "/notifications/devices", deviceTestBody, "", true, 401},
		{"email", "/notifications/devices", deviceTestBody, "fixture", false, 403},
		{"query", "/notifications/devices?userId=other", deviceTestBody, "fixture", true, 400},
		{"unknown", "/notifications/devices", strings.TrimSuffix(deviceTestBody, "}") + `,"userId":"other"}`, "fixture", true, 400},
		{"duplicate", "/notifications/devices", strings.TrimSuffix(deviceTestBody, "}") + `,"pushToken":"second"}`, "fixture", true, 400},
		{"trailing", "/notifications/devices", deviceTestBody + `{}`, "fixture", true, 400},
		{"UTF8", "/notifications/devices", strings.Replace(deviceTestBody, "fixture-private-token", string([]byte{255}), 1), "fixture", true, 400},
		{"limit", "/notifications/devices", strings.Replace(deviceTestBody, "fixture-private-token", strings.Repeat("a", 512<<10), 1), "fixture", true, 400},
		{"invalid token type", "/notifications/devices", strings.Replace(deviceTestBody, `"fixture-private-token"`, `false`, 1), "fixture", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &deviceHTTPStore{}
			router := deviceRouter(s, tc.verified, &bytes.Buffer{})
			req := httptest.NewRequest("POST", "/organizations/org_1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.want || s.calls != 0 {
				t.Fatalf("want %d before repository, got %d calls %d", tc.want, res.Code, s.calls)
			}
		})
	}
}
func TestDevicesRejectDeleteBodyIncludingChunked(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		s := &deviceHTTPStore{}
		router := deviceRouter(s, true, &bytes.Buffer{})
		req := httptest.NewRequest("DELETE", "/organizations/org_1/notifications/devices/"+deviceTestID, strings.NewReader(`{"pushToken":"forbidden"}`))
		req.Header.Set("Authorization", "Bearer fixture")
		if chunked {
			req.ContentLength = -1
			req.TransferEncoding = []string{"chunked"}
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != 400 || s.calls != 0 {
			t.Fatalf("chunked=%v: want rejection before mutation, got status=%d calls=%d", chunked, res.Code, s.calls)
		}
	}
}
func TestDevicesSanitizeRepositoryErrors(t *testing.T) {
	logs := &bytes.Buffer{}
	s := &deviceHTTPStore{fail: errors.New("fixture-private-database-detail")}
	router := deviceRouter(s, true, logs)
	req := httptest.NewRequest("POST", "/organizations/org_1/notifications/devices", strings.NewReader(deviceTestBody))
	req.Header.Set("Authorization", "Bearer fixture")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 500 || strings.Contains(res.Body.String(), "fixture-private") || strings.Contains(logs.String(), "fixture-private") {
		t.Fatal("repository failure was not sanitized")
	}
}
