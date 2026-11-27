package postgres

import (
	"testing"

	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
)

// Con V24: «Avisar a mi jefe» guarda el mensaje y avisa (con push) al ADMIN y al CHEF de la sede, no a quien lo envía;
// el contacto de WhatsApp prefiere al CHEF de la sede y cae al ADMIN.
func TestAvisarAMiJefeYContactoDeWhatsApp(t *testing.T) {
	ctx, pool := databaseWith(t, append(alertMigrations, "V21__alerts_incident_assigned.sql", "V23__alerts_incident_reported.sql", "V24__staff_messages_control_flagged_test_push.sql")...)
	s, _ := reportOne(t, ctx, pool, nil)
	before, beforeEvents := alertCount(t, ctx, pool)
	msg := domain.StaffMessage{ID: uid(8, 1), LocationID: site, Kind: "SUPPLY", Text: "Falta aceite"}
	if _, e := s.SendStaffMessage(ctx, "worker", org, "staff-key-1", msg); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendStaffMessage(ctx, "worker", org, "staff-key-1", msg); e != nil {
		t.Fatal("la misma clave debe repetir la respuesta", e)
	}
	a, ev := alertCount(t, ctx, pool)
	if a-before != 2 || ev-beforeEvents != 2 {
		t.Fatal("dos avisos con push", a-before, ev-beforeEvents)
	}
	var title, message string
	var authorized, pending bool
	if e := pool.QueryRow(ctx, `SELECT title,message,alert_recipient_authorized($1,id,recipient_user_id),alert_source_pending($1,id) FROM alerts WHERE alert_type='STAFF_MESSAGE' AND recipient_user_id='chef'`, org).Scan(&title, &message, &authorized, &pending); e != nil ||
		title != "Aviso del equipo: Falta insumo" || message != "Principal · Falta aceite" || !authorized || !pending {
		t.Fatal(title, message, authorized, pending, e)
	}
	if _, e := s.SendStaffMessage(ctx, "outside", org, "staff-key-2", domain.StaffMessage{ID: uid(8, 2), LocationID: site, Kind: "HELP"}); e == nil {
		t.Fatal("alguien de otra empresa no puede avisar")
	}
	if c, e := s.SupervisorContact(ctx, "worker", org, site); e != nil || c.WhatsApp != "" {
		t.Fatal("sin números no hay contacto", c, e)
	}
	if _, e := pool.Exec(ctx, `UPDATE application_users SET phone_number=CASE id WHEN 'admin' THEN '51911111111' WHEN 'chef2' THEN '51922222222' END WHERE id IN ('admin','chef2')`); e != nil {
		t.Fatal(e)
	}
	if c, e := s.SupervisorContact(ctx, "worker", org, site); e != nil || c.WhatsApp != "51911111111" || c.Role != "ADMIN" {
		t.Fatal("sin jefe con número cae al administrador (el jefe de otra sede no cuenta)", c, e)
	}
	if _, e := pool.Exec(ctx, `UPDATE application_users SET phone_number='51933333333' WHERE id='chef'`); e != nil {
		t.Fatal(e)
	}
	if c, e := s.SupervisorContact(ctx, "worker", org, site); e != nil || c.WhatsApp != "51933333333" || c.Role != "CHEF" {
		t.Fatal("prefiere al jefe de la sede", c, e)
	}
}
