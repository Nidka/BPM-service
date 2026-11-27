package postgres

import (
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
)

// Con V23: al reportar, avisa al ADMIN y al CHEF de la sede (no al CHEF de otra sede ni a quien reportó), con evento
// de salida, autorización y vigencia SQL; al asignar la incidencia el aviso deja de estar pendiente.
func TestReportarAvisaAlJefeYAlAdministrador(t *testing.T) {
	ctx, pool := databaseWith(t, append(alertMigrations, "V21__alerts_incident_assigned.sql", "V23__alerts_incident_reported.sql")...)
	s, r := reportOne(t, ctx, pool, nil)
	rows, e := pool.Query(ctx, `SELECT recipient_user_id,title,message,alert_type_ok FROM (SELECT recipient_user_id,title,message,alert_type='INCIDENT_REPORTED' AND source_type='INCIDENT' AND source_id=$1 AS alert_type_ok FROM alerts) a ORDER BY recipient_user_id`, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	var got []string
	for rows.Next() {
		var user, title, message string
		var ok bool
		if e = rows.Scan(&user, &title, &message, &ok); e != nil {
			t.Fatal(e)
		}
		if !ok || title != "Nuevo problema reportado: Superficie sucia" || message != "Principal · Revísalo y asigna quién lo corrige." {
			t.Fatal("aviso inesperado", user, title, message)
		}
		got = append(got, user)
	}
	rows.Close()
	if len(got) != 2 || got[0] != "admin" || got[1] != "chef" {
		t.Fatal("destinatarios inesperados", got)
	}
	if a, ev := alertCount(t, ctx, pool); a != 2 || ev != 0 {
		t.Fatal("prioridad normal: dos avisos en la bandeja, sin push", a, ev)
	}
	var authorized, pending bool
	if e = pool.QueryRow(ctx, `SELECT alert_recipient_authorized($1,id,recipient_user_id),alert_source_pending($1,id) FROM alerts WHERE recipient_user_id='chef' AND source_id=$2`, org, r.ID).Scan(&authorized, &pending); e != nil || !authorized || !pending {
		t.Fatal("el aviso debe estar autorizado y pendiente", authorized, pending, e)
	}
	if _, e = s.Act(ctx, "admin", org, r.ID, "assign-key-1", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Limpiar", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, `SELECT alert_source_pending($1,id) FROM alerts WHERE recipient_user_id='chef' AND source_id=$2`, org, r.ID).Scan(&pending); e != nil || pending {
		t.Fatal("asignada la incidencia, el aviso ya no está pendiente", pending, e)
	}
	urgent := domain.Report{ID: uid(4, 2), LocationID: site, CatalogID: catalogID, Description: "Fuga de gas", Priority: "urgent", ClientOperationID: uid(5, 2), ClientCreatedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if _, e = s.Report(ctx, "worker", org, "report-key-2", urgent); e != nil {
		t.Fatal(e)
	}
	var title string
	var push bool
	if e = pool.QueryRow(ctx, `SELECT title,(context_snapshot->>'pushEnabled')::boolean FROM alerts WHERE recipient_user_id='admin' AND source_id=$1`, urgent.ID).Scan(&title, &push); e != nil || title != "Problema urgente: Superficie sucia" || !push {
		t.Fatal("aviso urgente inesperado", title, push, e)
	}
	if a, ev := alertCount(t, ctx, pool); a != 5 || ev != 3 { // incluye el aviso de asignación (V21) de la primera incidencia
		t.Fatal("urgente: dos avisos más, con push", a, ev)
	}
	if _, e = s.Report(ctx, "worker", org, "report-key-3", domain.Report{ID: uid(4, 3), LocationID: site, CatalogID: catalogID, Description: "x", Priority: "ALTA", ClientOperationID: uid(5, 3), ClientCreatedAt: time.Now().UTC().Add(-time.Minute)}); e == nil {
		t.Fatal("prioridad desconocida debe rechazarse")
	}
}
