package fcm

import (
	"context"
	"encoding/json"
	"errors"
	firebase "firebase.google.com/go/v4"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"google.golang.org/api/option"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

type fcmTransport func(*http.Request) (*http.Response, error)

func (f fcmTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fcmFixture(t *testing.T, status int, body string, inspect func(*http.Request)) *Provider {
	t.Helper()
	client := &http.Client{Transport: fcmTransport(func(r *http.Request) (*http.Response, error) {
		if inspect != nil {
			inspect(r)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	app, err := firebase.NewApp(context.Background(), &firebase.Config{ProjectID: "fixture-project"}, option.WithHTTPClient(client), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	messaging, err := app.Messaging(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return New(messaging)
}

// payloadOf envía el mensaje al proveedor falso y devuelve el cuerpo JSON que habría recibido FCM.
func payloadOf(t *testing.T, m domain.Message) (notification map[string]string, data map[string]string, android map[string]any) {
	t.Helper()
	calls := 0
	p := fcmFixture(t, 200, `{"name":"projects/fixture-project/messages/accepted"}`, func(r *http.Request) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "fcm.googleapis.com" {
			t.Fatal("unexpected provider destination")
		}
		var body struct {
			Message map[string]json.RawMessage `json:"message"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid provider payload")
		}
		if json.Unmarshal(body.Message["notification"], &notification) != nil || json.Unmarshal(body.Message["data"], &data) != nil || json.Unmarshal(body.Message["android"], &android) != nil {
			t.Fatal("unexpected provider payload shape")
		}
	})
	if _, err := p.Send(context.Background(), "stable", m); err != nil || calls != 1 {
		t.Fatal("fixture push rejected", err)
	}
	return
}
func TestFCMSpecificPayloadKeepsLegacyKeys(t *testing.T) {
	notification, data, android := payloadOf(t, domain.Message{PushToken: "fixture-token", AlertID: "alert", OrganizationID: "org", RecipientHash: "uid-hash", Subject: "private subject", Text: "private notes",
		Push: domain.PushContent{Title: "Temperatura fuera de rango · Sede Centro", Body: "Pollo (Carnes): 12 °C", Type: "TEMPERATURE_DANGER", SourceType: "RECEIPT_ITEM", EntityID: "item", SiteID: "site", Route: "empresa/operacion"}})
	if notification["title"] != "Temperatura fuera de rango · Sede Centro" || notification["body"] != "Pollo (Carnes): 12 °C" {
		t.Fatal("texto específico ausente", notification)
	}
	want := map[string]string{"alertId": "alert", "organizationId": "org", "recipientHash": "uid-hash", "type": "TEMPERATURE_DANGER", "sourceType": "RECEIPT_ITEM", "entityId": "item", "siteId": "site", "route": "empresa/operacion"}
	if len(data) != len(want) {
		t.Fatal("claves inesperadas", data)
	}
	for k, v := range want {
		if data[k] != v {
			t.Fatal("clave de navegación incorrecta", k, data)
		}
	}
	for k, v := range data {
		if strings.Contains(v, "private") || strings.Contains(v, "fixture-token") {
			t.Fatal("dato privado en data", k)
		}
	}
	n, _ := android["notification"].(map[string]any)
	if android["collapse_key"] != "alert" || android["ttl"] != "3600s" || n["tag"] != "alert" || n["channel_id"] != "bpm-alerts" {
		t.Fatal("missing push dedupe or expiry", android)
	}
}
func TestFCMUnknownTypeFallsBackToGenericText(t *testing.T) {
	notification, data, _ := payloadOf(t, domain.Message{PushToken: "fixture-token", AlertID: "alert", OrganizationID: "org", RecipientHash: "uid-hash"})
	if notification["title"] != domain.GenericPushTitle || notification["body"] != domain.GenericPushBody {
		t.Fatal("respaldo genérico ausente", notification)
	}
	if len(data) != 4 || data["route"] != "avisos" || data["alertId"] != "alert" {
		t.Fatal("datos de respaldo inesperados", data)
	}
}
func TestFCMTruncatesOversizedText(t *testing.T) {
	notification, _, _ := payloadOf(t, domain.Message{PushToken: "t", AlertID: "alert", Push: domain.PushContent{Title: strings.Repeat("ñ", 200), Body: strings.Repeat("é", 500)}})
	if utf8.RuneCountInString(notification["title"]) > domain.PushTitleMax || utf8.RuneCountInString(notification["body"]) > domain.PushBodyMax {
		t.Fatal("texto sin límite")
	}
}
func TestFCMPermanentAuthorizationDoesNotRetryAsUncertain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"permission", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"fixture-private-provider-details"}}`},
		{"authentication", 401, `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"fixture-private-provider-details"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fcmFixture(t, tc.status, tc.body, nil)
			_, err := p.Send(context.Background(), "stable", domain.Message{PushToken: "fixture-token"})
			if !errors.Is(err, application.ErrPermanent) {
				t.Fatal("permanent authorization error classified as retryable")
			}
			if strings.Contains(err.Error(), "fixture-private") {
				t.Fatal("private provider error exposed")
			}
		})
	}
}

func TestFCMUnregisteredTokenOnlyInvalidatesItsBinding(t *testing.T) {
	p := fcmFixture(t, 404, `{"error":{"code":404,"status":"NOT_FOUND","message":"fixture-private-provider-details","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, nil)
	_, err := p.Send(context.Background(), "stable", domain.Message{PushToken: "fixture-token"})
	if !errors.Is(err, application.ErrInvalidToken) {
		t.Fatal("unregistered token classification lost")
	}
	if strings.Contains(err.Error(), "fixture-private") {
		t.Fatal("private provider error exposed")
	}
}
