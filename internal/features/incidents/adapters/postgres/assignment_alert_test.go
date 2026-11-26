package postgres

import (
	"bytes"
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/application"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"strings"
	"testing"
	"time"
)

var alertMigrations = []string{"V7__alerts_notifications.sql", "V14__alerts_admin_operates.sql"}

// reportOne reporta la incidencia base y devuelve el servicio listo para asignar.
func reportOne(t *testing.T, ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (*application.Service, domain.Report) {
	t.Helper()
	seed(t, ctx, pool)
	s := application.New(New(pool).WithLogger(logger))
	c := domain.Catalog{ID: catalogID, Code: "TEST", Name: "Superficie sucia", Area: "Cocina", NormativeCriterion: "Criterio", RiskLevel: "LOW"}
	if _, e := s.CreateCatalog(ctx, "admin", org, "catalog-key", c); e != nil {
		t.Fatal(e)
	}
	r := domain.Report{ID: uid(4, 1), LocationID: site, CatalogID: catalogID, Description: "Reparar", ClientOperationID: uid(5, 1), ClientCreatedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if _, e := s.Report(ctx, "worker", org, "report-key", r); e != nil {
		t.Fatal(e)
	}
	return s, r
}

func alertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (alerts, events int) {
	t.Helper()
	if e := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM alerts),(SELECT count(*) FROM outbox_events WHERE event_type='notifications.alert.ready.v1')`).Scan(&alerts, &events); e != nil {
		t.Fatal(e)
	}
	return
}

// Sin V21 (despliegue del código antes de la migración) la asignación funciona igual, no crea el aviso y
// registra una sola advertencia sin datos personales.
func TestAsignarSinMigracionV21NoCreaAvisoNiRompe(t *testing.T) {
	ctx, pool := databaseWith(t, alertMigrations...)
	var logs bytes.Buffer
	s, r := reportOne(t, ctx, pool, slog.New(slog.NewTextHandler(&logs, nil)))
	ultimoAvisoNoDisponible.Store(0)
	v, e := s.Act(ctx, "admin", org, r.ID, "assign-key-1", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Limpiar", DueAt: time.Now().Add(time.Hour)})
	if e != nil || v.Status != "ASSIGNED" {
		t.Fatal("la asignación debe funcionar sin V21", v.Status, e)
	}
	if a, ev := alertCount(t, ctx, pool); a != 0 || ev != 0 {
		t.Fatal("no debe crear aviso sin V21", a, ev)
	}
	if !strings.Contains(logs.String(), "V21") || strings.Contains(logs.String(), "worker") {
		t.Fatalf("advertencia ausente o con datos personales: %s", logs.String())
	}
}

// Con un esquema aún más antiguo (sin V7: sin columnas de avisos) la asignación tampoco se rompe.
func TestAsignarConEsquemaSinAlertasNoRompe(t *testing.T) {
	ctx, pool := database(t)
	s, r := reportOne(t, ctx, pool, nil)
	if _, e := s.Act(ctx, "admin", org, r.ID, "assign-key-1", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Limpiar", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
}

// Con V21: aviso para el responsable, con evento de salida, autorización/vigencia SQL e idempotencia.
func TestAsignarCreaAvisoParaElResponsable(t *testing.T) {
	ctx, pool := databaseWith(t, append(alertMigrations, "V21__alerts_incident_assigned.sql")...)
	s, r := reportOne(t, ctx, pool, nil)
	assign := domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Limpiar", DueAt: time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)}
	if _, e := s.Act(ctx, "admin", org, r.ID, "assign-key-1", assign); e != nil {
		t.Fatal(e)
	}
	// Repetir la misma clave devuelve la respuesta original sin otro aviso.
	if _, e := s.Act(ctx, "admin", org, r.ID, "assign-key-1", assign); e != nil {
		t.Fatal(e)
	}
	if a, ev := alertCount(t, ctx, pool); a != 1 || ev != 1 {
		t.Fatal("debe existir un aviso y un evento", a, ev)
	}
	var id, user, typ, title, message, sourceType, sourceID, snapshot string
	if e := pool.QueryRow(ctx, `SELECT id::text,recipient_user_id,alert_type,title,message,source_type,source_id,context_snapshot::text FROM alerts`).Scan(&id, &user, &typ, &title, &message, &sourceType, &sourceID, &snapshot); e != nil {
		t.Fatal(e)
	}
	zone := assign.DueAt.In(mustZone(t, "America/Lima")).Format("02/01/2006 15:04")
	if user != "worker" || typ != "INCIDENT_ASSIGNED" || sourceType != "INCIDENT" || sourceID != r.ID || title != "Te asignaron una incidencia: Superficie sucia" || message != "Principal · Plazo "+zone {
		t.Fatal("aviso inesperado", user, typ, title, message, sourceType, sourceID)
	}
	for _, privado := range []string{"Admin", "Worker", "@example"} {
		if strings.Contains(title+message+snapshot, privado) {
			t.Fatal("dato personal en el aviso", privado)
		}
	}
	var ok, pending, other bool
	if e := pool.QueryRow(ctx, `SELECT alert_recipient_authorized($1,$2::uuid,'worker'),alert_source_pending($1,$2::uuid),alert_recipient_authorized($1,$2::uuid,'chef')`, org, id).Scan(&ok, &pending, &other); e != nil || !ok || !pending || other {
		t.Fatal("autorización/vigencia", ok, pending, other, e)
	}
	// Al iniciar la acción deja de ser vigente (no se envía un push tardío) pero sigue visible en la bandeja.
	if _, e := s.Act(ctx, "worker", org, r.ID, "start-key-1", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"}); e != nil {
		t.Fatal(e)
	}
	if e := pool.QueryRow(ctx, `SELECT alert_recipient_authorized($1,$2::uuid,'worker'),alert_source_pending($1,$2::uuid)`, org, id).Scan(&ok, &pending); e != nil || !ok || pending {
		t.Fatal("tras iniciar", ok, pending, e)
	}
}

// Quien se asigna a sí mismo no recibe aviso; el aviso al ADMIN responsable sí se crea con su rol.
func TestAsignarAutoasignacionNoAvisaYAdminSi(t *testing.T) {
	ctx, pool := databaseWith(t, append(alertMigrations, "V21__alerts_incident_assigned.sql")...)
	s, r := reportOne(t, ctx, pool, nil)
	if _, e := s.Act(ctx, "admin", org, r.ID, "assign-key-1", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "admin", Description: "Yo", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if a, _ := alertCount(t, ctx, pool); a != 0 {
		t.Fatal("la autoasignación no debe avisar", a)
	}
	// Un CHEF asigna la incidencia de otra sede a un ADMIN: el ADMIN recibe el aviso aunque no tenga sede.
	r2 := domain.Report{ID: uid(4, 2), LocationID: site, CatalogID: catalogID, Description: "Otra", ClientOperationID: uid(5, 2), ClientCreatedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if _, e := s.Report(ctx, "worker", org, "report-key-2", r2); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Act(ctx, "chef", org, r2.ID, "assign-key-2", domain.Command{OperationID: uid(6, 5), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 2), ResponsibleUserID: "admin", Description: "Revisar", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	var id string
	var ok bool
	if e := pool.QueryRow(ctx, `SELECT id::text,alert_recipient_authorized($1,id,'admin') FROM alerts WHERE recipient_user_id='admin'`, org).Scan(&id, &ok); e != nil || !ok {
		t.Fatal("el ADMIN responsable debe recibir el aviso", e)
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, e := time.LoadLocation(name)
	if e != nil {
		t.Fatal(e)
	}
	return l
}

// Un fallo al crear el aviso (aquí, una restricción que lo rechaza) se aísla en un savepoint: la asignación se
// confirma, la incidencia queda ASSIGNED y el aviso simplemente no se crea.
func TestAsignarFalloDelAvisoNoRompeLaAsignacion(t *testing.T) {
	ctx, pool := databaseWith(t, append(alertMigrations, "V21__alerts_incident_assigned.sql")...)
	var logs bytes.Buffer
	s, r := reportOne(t, ctx, pool, slog.New(slog.NewTextHandler(&logs, nil)))
	execSQL(t, ctx, pool, `ALTER TABLE alerts ADD CONSTRAINT rechaza_incidencias CHECK (alert_type<>'INCIDENT_ASSIGNED')`)
	v, e := s.Act(ctx, "admin", org, r.ID, "assign-key-1", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Limpiar", DueAt: time.Now().Add(time.Hour)})
	if e != nil || v.Status != "ASSIGNED" || len(v.Actions) != 1 {
		t.Fatal("la asignación no debe depender del aviso", v.Status, e)
	}
	var status string
	if e = pool.QueryRow(ctx, `SELECT status FROM incidents WHERE id=$1`, r.ID).Scan(&status); e != nil || status != "ASSIGNED" {
		t.Fatal("la asignación no se confirmó", status, e)
	}
	if a, ev := alertCount(t, ctx, pool); a != 0 || ev != 0 {
		t.Fatal("no debe quedar aviso a medias", a, ev)
	}
	if !strings.Contains(logs.String(), "sqlstate=23514") {
		t.Fatalf("falta la advertencia con el código SQLSTATE: %s", logs.String())
	}
}
