package sender

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
)

// Un destino en la red local (aquí, el propio servidor de pruebas) se bloquea antes de conectar.
func TestSendBlocksInternalAddresses(t *testing.T) {
	hit := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hit = true }))
	defer srv.Close()
	_, err := New().Send(context.Background(), domain.Endpoint{URL: srv.URL, Secret: "whsec_x"}, domain.Delivery{ID: "1", Type: domain.EventTest})
	if !errors.Is(err, ErrBlockedAddress) || hit {
		t.Fatalf("debía bloquear 127.0.0.1: err=%v llegó=%v", err, hit)
	}
}
