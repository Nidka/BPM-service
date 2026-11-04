package resend

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResendUsesFixedEndpointStableKeyAndFrozenBody(t *testing.T) {
	calls := 0
	var previous string
	c := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Idempotency-Key") != "stable" {
			t.Fatal("endpoint/key changed")
		}
		b, _ := io.ReadAll(r.Body)
		if calls > 1 && previous != string(b) {
			t.Fatal("retry changed request")
		}
		previous = string(b)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"accepted"}`))}, nil
	})}
	p := New(c, "test-key")
	m := domain.Message{From: "sender@example.invalid", To: "receiver@example.invalid", Subject: "Aviso BPM", Text: "Abre BPM"}
	for range 2 {
		id, e := p.Send(context.Background(), "stable", m)
		if e != nil || id != "accepted" {
			t.Fatal(id, e)
		}
	}
	if strings.Contains(previous, "test-key") {
		t.Fatal("secret included in body")
	}
}
func TestResendSanitizesProviderError(t *testing.T) {
	p := New(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`private provider body token`))}, nil
	})}, "test-key")
	_, e := p.Send(context.Background(), "key", domain.Message{})
	if e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal("unsafe error", e)
	}
}
