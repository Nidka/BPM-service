package resend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendBuildsRequestAndNeverFollowsRedirects(t *testing.T) {
	var got payload
	var auth, idem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, idem = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"id":"re_123"}`))
	}))
	defer server.Close()
	client := New(server.Client(), "re_key").WithEndpoint(server.URL)
	id, err := client.Send(context.Background(), Email{From: "BPM <noreply@appbpm.com>", To: "a@b.pe", ReplyTo: "ayuda@appbpm.com", Subject: "Hola", HTML: "<p>x</p>", Text: "x", IdempotencyKey: "k1"})
	if err != nil || id != "re_123" {
		t.Fatal(id, err)
	}
	if auth != "Bearer re_key" || idem != "k1" || got.To[0] != "a@b.pe" || got.ReplyTo != "ayuda@appbpm.com" || got.HTML != "<p>x</p>" || got.Text != "x" {
		t.Fatal("solicitud inesperada", auth, idem, got)
	}
}

func TestSendClassifiesFailures(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{{429, ErrTemporary}, {500, ErrTemporary}, {503, ErrTemporary}, {401, ErrRejected}, {422, ErrRejected}, {302, ErrRejected}}
	for _, tc := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://evil.example")
			w.WriteHeader(tc.status)
		}))
		_, err := New(server.Client(), "k").WithEndpoint(server.URL).Send(context.Background(), Email{From: "a@b.pe", To: "c@d.pe", Subject: "s", Text: "t"})
		server.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("estado %d: %v", tc.status, err)
		}
	}
}

func TestSendWithoutKeyOrWithHeaderInjection(t *testing.T) {
	if _, err := New(nil, "").Send(context.Background(), Email{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
	if _, err := New(nil, "k").Send(context.Background(), Email{From: "a@b.pe", To: "c@d.pe\nBcc: x@y.pe", Subject: "s", Text: "t"}); !errors.Is(err, ErrRejected) {
		t.Fatal(err)
	}
}

func TestSendRejectsConfirmationWithoutID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	if _, err := New(server.Client(), "k").WithEndpoint(server.URL).Send(context.Background(), Email{From: "a@b.pe", To: "c@d.pe", Subject: "s", Text: "t"}); !errors.Is(err, ErrTemporary) {
		t.Fatal(err)
	}
}
