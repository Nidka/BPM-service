package alertshttp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/features/alerts/application"
	"github.com/go-chi/chi/v5"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuleInputRequiresAllBooleansAndRejectsUnknownJSON(t *testing.T) {
	for _, raw := range []string{`{"id":"30000000-0000-0000-0000-000000000001","name":"Regla","type":"TRAINING_ASSIGNED","configuration":{},"active":true,"emailEnabled":false}`, `{"id":"30000000-0000-0000-0000-000000000001","name":"Regla","type":"TRAINING_ASSIGNED","configuration":{},"active":true,"emailEnabled":false,"pushEnabled":null}`, `{"active":true,"active":false}`, `{"unexpected":true}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		var v ruleInput
		if request.DecodeStrictJSON(w, r, &v, 512<<10) && v.valid(true) {
			t.Fatal("accepted", raw)
		}
	}
}

type verifier struct{}

func (verifier) VerifyIDToken(context.Context, string) (contracts.Identity, error) {
	return contracts.Identity{UID: "chef", EmailVerified: true}, nil
}

type unavailable struct{}

func (unavailable) Within(context.Context, func(application.Store) error) error {
	return errors.New("private SQL password")
}
func TestClosedRoutesAndSanitizedFailures(t *testing.T) {
	router := chi.NewRouter()
	New(application.New(unavailable{}), nil).Register(router, middleware.Authenticate(verifier{}))
	base := "/organizations/test"
	for _, v := range []struct {
		method, path, body string
		want               int
	}{{"GET", base + "/alerts?actor=admin", "", 400}, {"GET", base + "/alerts?state=ALL&state=ALL", "", 400}, {"GET", base + "/alerts/unread-count?query=x", "", 400}, {"GET", base + "/alerts/unread-count", "x", 400}, {"GET", base + "/alerts/notuuid", "", 404}, {"POST", base + "/alerts/30000000-0000-0000-0000-000000000001/view", `{"actor":"admin"}`, 400}, {"POST", base + "/alerts/30000000-0000-0000-0000-000000000001/attend", `null`, 400}, {"POST", base + "/alert-rules", `{"id":"30000000-0000-0000-0000-000000000001","name":"Regla","type":"TRAINING_ASSIGNED","configuration":{"unknown":1},"active":true,"emailEnabled":false,"pushEnabled":false}`, 422}, {"GET", base + "/alerts", "", 500}} {
		r := httptest.NewRequest(v.method, v.path, strings.NewReader(v.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer test")
		r.Header.Set("Idempotency-Key", "stable-request")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != v.want {
			t.Fatalf("%s %s: %d %s", v.method, v.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private SQL") || strings.Contains(w.Body.String(), "password") {
			t.Fatal("internal details leaked")
		}
	}
}

func TestUnexpectedReadBodyProducesSingleJSONError(t *testing.T) {
	router := chi.NewRouter()
	New(application.New(unavailable{}), nil).Register(router, middleware.Authenticate(verifier{}))
	for _, path := range []string{"/organizations/test/alerts/unread-count", "/organizations/test/alerts/30000000-0000-0000-0000-000000000001"} {
		r := httptest.NewRequest("GET", path, strings.NewReader("unexpected"))
		r.Header.Set("Authorization", "Bearer test")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 400 || !json.Valid(w.Body.Bytes()) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
