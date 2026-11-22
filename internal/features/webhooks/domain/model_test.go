package domain

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAcceptsOnlyPublicHTTPS(t *testing.T) {
	ok, err := Normalize(Input{URL: " https://n8n.miempresa.pe/webhook/bpm ", EventTypes: []string{"TEMPERATURE_DANGER", "TEMPERATURE_DANGER"}})
	if err != nil || ok.URL != "https://n8n.miempresa.pe/webhook/bpm" || len(ok.EventTypes) != 1 {
		t.Fatalf("válida: %+v %v", ok, err)
	}
	for _, bad := range []string{"http://n8n.pe/x", "https://localhost/x", "https://127.0.0.1/x", "https://10.0.0.5/x", "https://169.254.169.254/latest",
		"https://user:pass@n8n.pe/x", "https://intranet/x", "https://n8n.internal/x", "ftp://n8n.pe"} {
		if _, err := Normalize(Input{URL: bad, EventTypes: []string{"TEMPERATURE_DANGER"}}); err == nil {
			t.Errorf("%s debía rechazarse", bad)
		}
	}
	if _, err := Normalize(Input{URL: "https://n8n.pe/x"}); err == nil {
		t.Error("sin tipos de evento")
	}
	if _, err := Normalize(Input{URL: "https://n8n.pe/x", EventTypes: []string{"OTRO"}}); err == nil {
		t.Error("tipo desconocido")
	}
}

func TestPublicIPBlocksInternalRanges(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fc00::1", "fe80::1"} {
		if PublicIP(net.ParseIP(ip)) {
			t.Errorf("%s no es pública", ip)
		}
	}
	if !PublicIP(net.ParseIP("8.8.8.8")) {
		t.Error("8.8.8.8 es pública")
	}
}

func TestSignatureIsStableHMAC(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	a := Signature("whsec_x", at, []byte(`{"a":1}`))
	if !strings.HasPrefix(a, "sha256=") || len(a) != 7+64 || a != Signature("whsec_x", at, []byte(`{"a":1}`)) || a == Signature("whsec_y", at, []byte(`{"a":1}`)) {
		t.Fatalf("firma: %s", a)
	}
}
