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
func TestFCMDataOnlyGenericPayload(t *testing.T) {
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
		if _, found := body.Message["notification"]; found {
			t.Fatal("push contains visible private notification")
		}
		var data map[string]string
		if json.Unmarshal(body.Message["data"], &data) != nil || len(data) != 3 || data["alertId"] != "alert" || data["organizationId"] != "org" || data["recipientHash"] != "uid-hash" {
			t.Fatal("unexpected public push data")
		}
		var android map[string]any
		if json.Unmarshal(body.Message["android"], &android) != nil || android["collapse_key"] != "alert" || android["ttl"] != "3600s" {
			t.Fatal("missing push dedupe or expiry")
		}
	})
	_, err := p.Send(context.Background(), "stable", domain.Message{PushToken: "fixture-token", AlertID: "alert", OrganizationID: "org", RecipientHash: "uid-hash", Subject: "private subject", Text: "private notes"})
	if err != nil || calls != 1 {
		t.Fatal("fixture push rejected", err)
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
