package app

import (
	"context"
	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/alerts"
	"github.com/Nidka/BPM-service/internal/features/checklists"
	"github.com/Nidka/BPM-service/internal/features/normative"
	"github.com/Nidka/BPM-service/internal/features/notifications"
	"github.com/Nidka/BPM-service/internal/features/organizations"
	"github.com/Nidka/BPM-service/internal/features/platformsettings"
	"github.com/Nidka/BPM-service/internal/features/reports"
	"github.com/Nidka/BPM-service/internal/features/research"
	"github.com/Nidka/BPM-service/internal/features/training"
	"github.com/Nidka/BPM-service/internal/features/usage"
	"github.com/Nidka/BPM-service/internal/features/webhooks"
	"github.com/go-chi/chi/v5"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type validToken struct{}

func (validToken) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "person", EmailVerified: true}, nil
}

type expiredSession struct{}

func (expiredSession) Validate(context.Context, contracts.Identity, bool) (contracts.SessionWindow, error) {
	return contracts.SessionWindow{}, contracts.ErrSessionExpired
}

func TestBusinessModulesDenyExpiredSessionsBeforeDatabaseAccess(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := chi.NewRouter()
	organizations.Routes(nil, validToken{}, logger, expiredSession{})(r)
	checklists.Routes(nil, validToken{}, logger, expiredSession{})(r)
	alerts.Routes(nil, validToken{}, logger, expiredSession{})(r)
	notifications.Routes(nil, validToken{}, logger, expiredSession{})(r)
	training.Routes(nil, validToken{}, nil, config.R2Config{}, config.HTTPConfig{}, logger, expiredSession{})(r)
	reports.Routes(nil, validToken{}, logger, expiredSession{})(r)
	usage.Routes(nil, validToken{}, logger, expiredSession{})(r)
	research.Routes(nil, validToken{}, logger, expiredSession{})(r)
	webhooks.Routes(nil, validToken{}, logger, expiredSession{})(r)
	normative.Routes(nil, validToken{}, logger, expiredSession{})(r)
	platformsettings.Routes(nil, validToken{}, logger, expiredSession{})(r)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/organizations/org/alerts"}, {"GET", "/organizations/org/alerts/unread-count"},
		{"GET", "/organizations/org/alerts/00000000-0000-0000-0000-000000000001"}, {"POST", "/organizations/org/alerts/00000000-0000-0000-0000-000000000001/view"}, {"POST", "/organizations/org/alerts/00000000-0000-0000-0000-000000000001/attend"},
		{"GET", "/organizations/org/alert-rules"}, {"POST", "/organizations/org/alert-rules"}, {"PUT", "/organizations/org/alert-rules/00000000-0000-0000-0000-000000000001"},
		{"POST", "/organizations/org/notifications/devices"}, {"POST", "/organizations/org/notifications/test"}, {"DELETE", "/organizations/org/notifications/devices/00000000-0000-0000-0000-000000000001"},
		{"GET", "/organizations"}, {"POST", "/organizations/org/members"},
		{"GET", "/organizations/org/checklists"}, {"POST", "/organizations/org/checklist-runs"},
		{"PUT", "/organizations/org/checklist-runs/run/answers"}, {"POST", "/organizations/org/checklist-runs/run/submit"},
		{"GET", "/organizations/org/training/courses"}, {"POST", "/organizations/org/training/courses/00000000-0000-0000-0000-000000000001/attempts"},
		{"PUT", "/organizations/org/training/courses/00000000-0000-0000-0000-000000000001/draft"}, {"POST", "/organizations/org/training/videos/uploads"},
		{"GET", "/organizations/org/reports/compliance"}, {"GET", "/organizations/org/reports/summary"}, {"PUT", "/organizations/org/reports/settings"}, {"POST", "/organizations/org/reports/certificates"},
		{"POST", "/organizations/org/usage/events"}, {"GET", "/organizations/org/usage/summary"},
		{"GET", "/organizations/org/research/participation"}, {"POST", "/organizations/org/research/consent"}, {"POST", "/organizations/org/research/submissions"},
		{"GET", "/research/access"}, {"GET", "/research/instruments"}, {"GET", "/research/results"},
		{"GET", "/organizations/org/webhooks"}, {"POST", "/organizations/org/webhooks"}, {"POST", "/organizations/org/webhooks/00000000-0000-0000-0000-000000000001/test"},
		{"GET", "/normative/status"}, {"GET", "/normative/search?q=pollo"}, {"GET", "/normative/documents"}, {"POST", "/normative/documents"},
		{"PUT", "/normative/documents/00000000-0000-0000-0000-000000000001"}, {"GET", "/platform/support"}, {"PUT", "/platform/support"}, {"POST", "/normative/documents/00000000-0000-0000-0000-000000000001/archive"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer token")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != 401 || !strings.Contains(rec.Body.String(), "SESSION_EXPIRED") {
				t.Fatal("module did not enforce session", rec.Code, rec.Body.String())
			}
		})
	}
}
