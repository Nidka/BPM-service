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
	for _, n := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql", "V7__alerts_notifications.sql"} {
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
