package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

const devID = "10000000-0000-0000-0000-000000000001"
const installID = "20000000-0000-0000-0000-000000000001"
const alertID = "30000000-0000-0000-0000-000000000001"

func fixture(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := pgx.Identifier{fmt.Sprintf("notifications_%d", time.Now().UnixNano())}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close(ctx) })
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 8
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	for _, n := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql", "V7__alerts_notifications.sql", "V14__alerts_admin_operates.sql", "V21__alerts_incident_assigned.sql", "V23__alerts_incident_reported.sql", "V24__staff_messages_control_flagged_test_push.sql"} {
		b, e := os.ReadFile("../../../../../migraciones/" + n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.Exec(ctx, string(b)); e != nil {
			t.Fatal(n, e)
		}
	}
	exec(t, ctx, p, `INSERT INTO organizations(id,legal_name,business_type) VALUES('org','Empresa','RESTAURANT'),('other','Otra','RESTAURANT');INSERT INTO application_users(id,email,display_name) VALUES('u','u@example.invalid','Persona'),('v','v@example.invalid','Otra persona');INSERT INTO organization_memberships(organization_id,user_id,role) VALUES('org','u','ADMIN'),('other','u','ADMIN'),('other','v','ADMIN')`)
	exec(t, ctx, p, `INSERT INTO alerts(id,organization_id,recipient_user_id,alert_type,title,message,source_type,source_id,status,viewed_at,context_snapshot) VALUES($1,'org','u','ORGANIZATION_INACTIVE','Aviso','Revisa BPM','ORGANIZATION','org','VIEWED',NOW(),jsonb_build_object('emailEnabled',true,'pushEnabled',true,'lastActivity',NOW()::text))`, alertID)
	return ctx, p
}
func exec(t *testing.T, ctx context.Context, p *pgxpool.Pool, q string, a ...any) {
	t.Helper()
	if _, e := p.Exec(ctx, q, a...); e != nil {
		t.Fatal(e)
	}
}
func input(token string) domain.DeviceInput {
	return domain.DeviceInput{ID: devID, InstallationID: installID, Platform: "ANDROID", PushToken: token}
}
func TestDevicePrivateBindingRotationAndIdempotentLogout(t *testing.T) {
	ctx, p := fixture(t)
	s := application.NewDevices(New(p))
	if _, e := s.Register(ctx, "u", "org", input("token-one")); e != nil {
		t.Fatal(e)
	}
	other := input("token-one")
	other.ID = "10000000-0000-0000-0000-000000000002"
	if _, e := s.Register(ctx, "v", "other", other); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("token shared between users", e)
	}
	if _, e := s.Register(ctx, "u", "other", other); e != nil {
		t.Fatal("same user multi-org rejected", e)
	}
	if _, e := s.Deactivate(ctx, "v", "other", devID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("foreign device exposed", e)
	}
	if _, e := s.Register(ctx, "u", "org", input("token-two")); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		d, e := s.Deactivate(ctx, "u", "org", devID)
		if e != nil || d.Status != "INACTIVE" {
			t.Fatal(d, e)
		}
	}
	exec(t, ctx, p, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id='org' AND user_id='u'`)
	if _, e := s.Register(ctx, "u", "org", input("token-three")); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("revoked user registered", e)
	}
}
func TestDeliverySnapshotPrivateRecheckAndStateIndependent(t *testing.T) {
	ctx, p := fixture(t)
	r := New(p)
	s := application.NewDevices(r)
	if _, e := s.Register(ctx, "u", "org", input("one")); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	for range 2 {
		if e := r.Stage(ctx, "org", alertID, "sender@example.invalid", now); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries`).Scan(&n); e != nil || n != 2 {
		t.Fatal("stage duplicated", n, e)
	}
	if v, e := r.Claim(ctx, nil, now); e != nil || v != nil {
		t.Fatal("unconfigured provider consumed attempt", e)
	}
	v, e := r.Claim(ctx, []string{"EMAIL"}, now)
	if e != nil || v == nil {
		t.Fatal(v, e)
	}
	if ok, e := r.Eligible(ctx, *v); e != nil || !ok {
		t.Fatal("eligible rejected", ok, e)
	}
	if e = r.Finish(ctx, *v, domain.Outcome{Status: "SENT", ProviderID: "accepted"}, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = p.QueryRow(ctx, `SELECT status FROM alerts WHERE id=$1`, alertID).Scan(&status); e != nil || status != "VIEWED" {
		t.Fatal("provider overwrote view", status, e)
	}
	v, e = r.Claim(ctx, []string{"FCM"}, now)
	if e != nil || v == nil {
		t.Fatal(v, e)
	}
	if _, e = s.Register(ctx, "u", "org", input("rotated")); e != nil {
		t.Fatal(e)
	}
	if ok, e := r.Eligible(ctx, *v); e != nil || ok {
		t.Fatal("stale token remained eligible", ok, e)
	}
	if e = r.Finish(ctx, *v, domain.Outcome{Status: "CANCELLED", InvalidToken: true}, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `SELECT status FROM mobile_devices WHERE id=$1`, devID).Scan(&status); e != nil || status != "ACTIVE" {
		t.Fatal("late provider error invalidated new token", status, e)
	}
}
func TestDeliveryLeaseFenceAndRevocation(t *testing.T) {
	ctx, p := fixture(t)
	r := New(p)
	now := time.Now().UTC()
	if e := r.Stage(ctx, "org", alertID, "sender@example.invalid", now); e != nil {
		t.Fatal(e)
	}
	v, e := r.Claim(ctx, []string{"EMAIL"}, now)
	if e != nil || v == nil {
		t.Fatal(v, e)
	}
	exec(t, ctx, p, `UPDATE notification_deliveries SET lease_token=gen_random_uuid() WHERE id=$1`, v.ID)
	if e = r.Finish(ctx, *v, domain.Outcome{Status: "SENT"}, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	var status string
	p.QueryRow(ctx, `SELECT status FROM notification_deliveries WHERE id=$1`, v.ID).Scan(&status)
	if status != "PROCESSING" {
		t.Fatal("stale worker completed lease", status)
	}
	exec(t, ctx, p, `UPDATE notification_deliveries SET locked_until=NOW()-interval '1 second' WHERE id=$1`, v.ID)
	v, e = r.Claim(ctx, []string{"EMAIL"}, time.Now().UTC())
	if e != nil || v == nil {
		t.Fatal("expired lease stranded", v, e)
	}
	exec(t, ctx, p, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id='org' AND user_id='u'`)
	if ok, e := r.Eligible(ctx, *v); e != nil || ok {
		t.Fatal("revocation ignored", ok, e)
	}
	exec(t, ctx, p, `UPDATE notification_deliveries SET locked_until=NOW()-interval '1 second',created_at=NOW()-interval '25 hours' WHERE id=$1`, v.ID)
	if v, e = r.Claim(ctx, []string{"EMAIL"}, time.Now().UTC()); e != nil || v != nil {
		t.Fatal("expired uncertain delivery resent", v, e)
	}
	p.QueryRow(ctx, `SELECT status FROM notification_deliveries LIMIT 1`).Scan(&status)
	if status != "UNCERTAIN" {
		t.Fatal(status)
	}
}
func TestOutboxLeaseRecoveryDedupAndExpiry(t *testing.T) {
	ctx, p := fixture(t)
	now := time.Now().UTC()
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	event := outbox.Event{OrganizationID: "org", Type: "test", AggregateType: "alert", AggregateID: alertID, DedupKey: "key", CreatedAt: now}
	for range 2 {
		if e = outbox.Enqueue(ctx, tx, event); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	q := outbox.NewPostgres(p)
	rows, e := q.Claim(ctx, []string{"test"}, now, 1, time.Minute)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	old := rows[0]
	if rows, e = q.Claim(ctx, []string{"test"}, now, 1, time.Minute); e != nil || len(rows) != 0 {
		t.Fatal("two workers claimed same lease", rows, e)
	}
	rows, e = q.Claim(ctx, []string{"test"}, now.Add(2*time.Minute), 1, time.Minute)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if e = q.Complete(ctx, old, now.Add(2*time.Minute)); e != nil {
		t.Fatal(e)
	}
	var status string
	p.QueryRow(ctx, `SELECT status FROM outbox_events WHERE id=$1`, old.ID).Scan(&status)
	if status != "PROCESSING" {
		t.Fatal("old lease completed", status)
	}
	if e = q.Complete(ctx, rows[0], now.Add(2*time.Minute)); e != nil {
		t.Fatal(e)
	}
	p.QueryRow(ctx, `SELECT status FROM outbox_events WHERE id=$1`, old.ID).Scan(&status)
	if status != "PROCESSED" {
		t.Fatal(status)
	}
	exec(t, ctx, p, `UPDATE outbox_events SET status='PROCESSING',attempts=8,locked_until=$2 WHERE id=$1`, old.ID, now)
	if rows, e = q.Claim(ctx, []string{"test"}, now.Add(time.Minute), 1, time.Minute); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	p.QueryRow(ctx, `SELECT status FROM outbox_events WHERE id=$1`, old.ID).Scan(&status)
	if status != "FAILED" {
		t.Fatal("max-attempt lease stranded", status)
	}
}

func TestPushTargetsReconcileAfterRotation(t *testing.T) {
	ctx, p := fixture(t)
	r := New(p)
	devices := application.NewDevices(r)
	if _, e := devices.Register(ctx, "u", "org", input("old")); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	if e := r.Stage(ctx, "org", alertID, "sender@example.invalid", now); e != nil {
		t.Fatal(e)
	}
	if _, e := devices.Register(ctx, "u", "org", input("new")); e != nil {
		t.Fatal(e)
	}
	if e := r.Reconcile(ctx, now); e != nil {
		t.Fatal(e)
	}
	var n int
	e := p.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE channel='FCM' AND request_payload->>'tokenHash'=$1`, tokenHash("new")).Scan(&n)
	if e != nil || n != 1 {
		t.Fatal("replacement target missing", n, e)
	}
	if e = r.Reconcile(ctx, now); e != nil {
		t.Fatal(e)
	}
	p.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE channel='FCM'`).Scan(&n)
	if n != 2 {
		t.Fatal("reconciliation duplicated target", n)
	}
}

// TestStagePushEspecificoPorTipo prepara avisos de cada tipo con datos reales de la base y verifica que el
// texto y la navegación del push se guarden sin datos personales (el usuario de prueba se llama «Persona»).
func TestStagePushEspecificoPorTipo(t *testing.T) {
	ctx, p := fixture(t)
	r := New(p)
	if _, e := application.NewDevices(r).Register(ctx, "u", "org", input("one")); e != nil {
		t.Fatal(e)
	}
	const site, tpl, asg, sup, prod, rec, item, course, tasg = "70000000-0000-0000-0000-000000000001", "70000000-0000-0000-0000-000000000002", "70000000-0000-0000-0000-000000000003", "70000000-0000-0000-0000-000000000004", "70000000-0000-0000-0000-000000000005", "70000000-0000-0000-0000-000000000006", "70000000-0000-0000-0000-000000000007", "70000000-0000-0000-0000-000000000008", "70000000-0000-0000-0000-000000000009"
	exec(t, ctx, p, `INSERT INTO locations(id,organization_id,name,address,timezone)VALUES($1,'org','Sede Centro','Calle 1','America/Lima')`, site)
	exec(t, ctx, p, `INSERT INTO checklist_templates(id,organization_id,name,area)VALUES($1,'org','Apertura de cocina','COCINA')`, tpl)
	exec(t, ctx, p, `INSERT INTO checklist_assignments(id,organization_id,location_id,template_id,frequency)VALUES($1,'org',$2,$3,'DAILY')`, asg, site, tpl)
	exec(t, ctx, p, `INSERT INTO suppliers(id,organization_id,name)VALUES($1,'org','Proveedor')`, sup)
	exec(t, ctx, p, `INSERT INTO products(id,organization_id,name,category,unit)VALUES($1,'org','Pollo','Carnes','kg')`, prod)
	exec(t, ctx, p, `INSERT INTO supply_receipts(id,organization_id,location_id,supplier_id,received_by,received_at,client_operation_id,client_created_at)VALUES($1,'org',$2,$3,'u',NOW(),$4,NOW())`, rec, site, sup, "70000000-0000-0000-0000-00000000000a")
	exec(t, ctx, p, `INSERT INTO supply_receipt_items(id,organization_id,receipt_id,product_id,quantity,temperature_celsius,decision)VALUES($1,'org',$2,$3,1,12,'ACCEPTED')`, item, rec, prod)
	exec(t, ctx, p, `INSERT INTO training_courses(id,organization_id,code,title)VALUES($1,'org','C1','Higiene personal')`, course)
	exec(t, ctx, p, `INSERT INTO training_assignments(id,organization_id,course_id,target_role)VALUES($1,'org',$2,'WORKER')`, tasg, course)
	const incident, action = "70000000-0000-0000-0000-00000000000b", "70000000-0000-0000-0000-00000000000c"
	seedIncident(t, ctx, p, site, incident, action)
	now := time.Now().UTC()
	for i, tc := range []struct {
		kind, sourceType, source, location, snapshot string
		title, body, route                           string
	}{
		{"TEMPERATURE_DANGER", "RECEIPT_ITEM", item, site, `{"temperatureCelsius":"12.00"}`, "Temperatura fuera de rango · Sede Centro", "Pollo (Carnes): 12 °C", "empresa/operacion"},
		{"CHECKLIST_MISSING", "CHECKLIST_ASSIGNMENT", asg, site, `{}`, "Control pendiente: Apertura de cocina", "Aún no se registra el control de hoy · Sede Centro", "control/programacion/" + asg},
		{"ORGANIZATION_INACTIVE", "ORGANIZATION", "org", "", `{"configuration":{"inactivityDays":5}}`, "Sin actividad reciente en BPM", "No hay actividad desde hace 5 días. Revisa la operación.", "empresa"},
		{"TRAINING_ASSIGNED", "TRAINING_ASSIGNMENT", tasg, "", `{}`, "Capacitación asignada: Higiene personal", "Complétala en la Academia.", "aprender"},
		{"INCIDENT_ASSIGNED", "INCIDENT", incident, site, `{"actionId":"` + action + `"}`, "Te asignaron una incidencia: Superficie sucia", "Sede Centro · Plazo 25/11/2026 20:30", "empresa/incidencias/" + incident},
		{"OTRO_TIPO", "", "", "", `{}`, domain.GenericPushTitle, domain.GenericPushBody, "avisos"},
	} {
		id := fmt.Sprintf("80000000-0000-0000-0000-%012d", i+1)
		snapshot := tc.snapshot[:len(tc.snapshot)-1] + `,"pushEnabled":true}`
		if tc.snapshot == `{}` {
			snapshot = `{"pushEnabled":true}`
		}
		exec(t, ctx, p, `INSERT INTO alerts(id,organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,context_snapshot)VALUES($1,'org',NULLIF($2,'')::uuid,'u',$3,'Aviso','Revisa BPM',NULLIF($4,''),NULLIF($5,''),$6::jsonb)`, id, tc.location, tc.kind, tc.sourceType, tc.source, snapshot)
		// Se prepara directamente: la autorización SQL de tipos ajenos al catálogo no es parte de esta prueba.
		tx, e := p.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		got, e := r.pushContent(ctx, tx, "org", id)
		tx.Rollback(ctx)
		if e != nil {
			t.Fatal(tc.kind, e)
		}
		if got.Title != tc.title || got.Body != tc.body || got.Route != tc.route || got.Type != tc.kind {
			t.Fatalf("%s: %+v", tc.kind, got)
		}
		if tc.location != "" && got.SiteID != tc.location {
			t.Fatal("siteId ausente", tc.kind)
		}
		for _, privado := range []string{"Persona", "u@example", "@"} {
			if strings.Contains(got.Title+got.Body, privado) {
				t.Fatal("dato personal en el push", tc.kind)
			}
		}
	}
	// El flujo completo conserva el texto en la entrega y lo devuelve al reclamarla. Se usa el aviso de
	// inactividad porque su vigencia en SQL solo depende de la última actividad.
	id := "80000000-0000-0000-0000-000000000003"
	exec(t, ctx, p, `UPDATE alerts SET context_snapshot=context_snapshot||'{"lastActivity":"2099-01-01T00:00:00Z"}' WHERE id=$1`, id)
	if e := r.Stage(ctx, "org", id, "", now); e != nil {
		t.Fatal(e)
	}
	v, e := r.Claim(ctx, []string{"FCM"}, now)
	if e != nil || v == nil {
		t.Fatal("entrega FCM ausente", v, e)
	}
	if v.Message.Push.Title != "Sin actividad reciente en BPM" || v.Message.Push.Route != "empresa" || v.Message.Push.Type != "ORGANIZATION_INACTIVE" || v.Message.Push.SourceType != "ORGANIZATION" {
		t.Fatalf("entrega sin texto específico: %+v", v.Message.Push)
	}
}

// seedIncident crea una incidencia ASSIGNED con una acción PENDING cuyo plazo es 25/11/2026 20:30 en Lima.
func seedIncident(t *testing.T, ctx context.Context, p *pgxpool.Pool, site, incident, action string) {
	t.Helper()
	const catalog = "70000000-0000-0000-0000-00000000000d"
	exec(t, ctx, p, `INSERT INTO incident_catalog(id,organization_id,code,name,area,normative_criterion,risk_level)VALUES($1,'org','SUP','Superficie sucia','Cocina','Criterio','LOW')`, catalog)
	exec(t, ctx, p, `INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,status,client_operation_id,client_created_at)VALUES($1,'org',$2,$3,'u','ASSIGNED',$4,NOW())`, incident, site, catalog, "70000000-0000-0000-0000-00000000000e")
	exec(t, ctx, p, `INSERT INTO corrective_actions(id,organization_id,incident_id,responsible_user_id,description,due_at)VALUES($1,'org',$2,'u','Limpiar','2026-11-26T01:30:00Z')`, action, incident)
}

// TestIncidentAssignedStageClaimYVigencia recorre el flujo completo del aviso de incidencia asignada: se prepara la
// entrega FCM con su texto, ruta e id de incidencia, y deja de ser vigente cuando el responsable inicia la acción.
func TestIncidentAssignedStageClaimYVigencia(t *testing.T) {
	ctx, p := fixture(t)
	r := New(p)
	if _, e := application.NewDevices(r).Register(ctx, "u", "org", input("one")); e != nil {
		t.Fatal(e)
	}
	const site, incident, action, id = "70000000-0000-0000-0000-000000000001", "70000000-0000-0000-0000-00000000000b", "70000000-0000-0000-0000-00000000000c", "80000000-0000-0000-0000-0000000000aa"
	exec(t, ctx, p, `INSERT INTO locations(id,organization_id,name,address,timezone)VALUES($1,'org','Sede Centro','Calle 1','America/Lima')`, site)
	seedIncident(t, ctx, p, site, incident, action)
	exec(t, ctx, p, `INSERT INTO alerts(id,organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,dedup_key,context_snapshot)VALUES($1,'org',$2,'u','INCIDENT_ASSIGNED','Aviso','Revisa BPM','INCIDENT',$3,'incident-assigned:'||$4::text,jsonb_build_object('actionId',$4::text,'pushEnabled',true,'emailEnabled',false))`, id, site, incident, action)
	var authorized, pending bool
	if e := p.QueryRow(ctx, `SELECT alert_recipient_authorized('org',$1,'u'),alert_source_pending('org',$1)`, id).Scan(&authorized, &pending); e != nil || !authorized || !pending {
		t.Fatal("el responsable debe estar autorizado y vigente", authorized, pending, e)
	}
	// Otro usuario de la misma empresa no queda autorizado a leer ese aviso.
	if e := p.QueryRow(ctx, `SELECT alert_recipient_authorized('org',$1,'v')`, id).Scan(&authorized); e != nil || authorized {
		t.Fatal("aviso visible para un tercero", e)
	}
	now := time.Now().UTC()
	if e := r.Stage(ctx, "org", id, "", now); e != nil {
		t.Fatal(e)
	}
	v, e := r.Claim(ctx, []string{"FCM"}, now)
	if e != nil || v == nil {
		t.Fatal("entrega FCM ausente", v, e)
	}
	got := v.Message.Push
	if got.Title != "Te asignaron una incidencia: Superficie sucia" || got.Body != "Sede Centro · Plazo 25/11/2026 20:30" || got.Route != "empresa/incidencias/"+incident || got.EntityID != incident || got.SiteID != site || got.Type != "INCIDENT_ASSIGNED" || got.SourceType != "INCIDENT" {
		t.Fatalf("push de incidencia: %+v", got)
	}
	if ok, e := r.Eligible(ctx, *v); e != nil || !ok {
		t.Fatal("la entrega debe ser elegible antes de iniciar", ok, e)
	}
	// Con la acción iniciada el aviso ya no es vigente y no se entrega.
	exec(t, ctx, p, `UPDATE corrective_actions SET status='IN_PROGRESS' WHERE id=$1`, action)
	if e := p.QueryRow(ctx, `SELECT alert_source_pending('org',$1)`, id).Scan(&pending); e != nil || pending {
		t.Fatal("el aviso sigue vigente tras iniciar", e)
	}
}
