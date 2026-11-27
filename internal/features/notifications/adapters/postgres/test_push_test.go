package postgres

import (
	"errors"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
)

// La prueba exige un celular activo, deja un solo aviso TEST_PUSH (solo para la persona) con su evento y limita a una por minuto.
func TestTestPushOwnDeviceRateLimited(t *testing.T) {
	ctx, p := fixture(t)
	s := application.NewDevices(New(p))
	if e := s.TestPush(ctx, "u", "org"); !errors.Is(e, domain.ErrNoDevice) {
		t.Fatal("sin celular activo", e)
	}
	if _, e := s.Register(ctx, "u", "org", input("token-test")); e != nil {
		t.Fatal(e)
	}
	if e := s.TestPush(ctx, "u", "org"); e != nil {
		t.Fatal(e)
	}
	if e := s.TestPush(ctx, "u", "org"); !errors.Is(e, domain.ErrRateLimited) {
		t.Fatal("una por minuto", e)
	}
	var n, events int
	var authorized, pending bool
	if e := p.QueryRow(ctx, `SELECT count(*),bool_and(alert_recipient_authorized('org',id,'u')),bool_and(alert_source_pending('org',id)),(SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id::text FROM alerts WHERE alert_type='TEST_PUSH')) FROM alerts WHERE alert_type='TEST_PUSH' AND recipient_user_id='u' AND location_id IS NULL AND title='Notificación de prueba'`).Scan(&n, &authorized, &pending, &events); e != nil || n != 1 || !authorized || !pending || events != 1 {
		t.Fatal(n, authorized, pending, events, e)
	}
	if e := s.TestPush(ctx, "v", "org"); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("solo miembros de la empresa", e)
	}
}
