package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/features/alerts/application"
	"github.com/Nidka/BPM-service/internal/features/alerts/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
	"time"
)

const org = "alerts-org"
const site = "20000000-0000-0000-0000-000000000001"

func uid(n int) string { return fmt.Sprintf("30000000-0000-0000-0000-%012d", n) }
func database(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := pgx.Identifier{fmt.Sprintf("alerts_%d", time.Now().UnixNano())}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	for _, f := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql", "V7__alerts_notifications.sql"} {
		b, e := os.ReadFile("../../../../../migraciones/" + f)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(b)); e != nil {
			t.Fatal(f, e)
		}
	}
	return ctx, pool
}
func exec(t *testing.T, ctx context.Context, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, e := p.Exec(ctx, sql, args...); e != nil {
		t.Fatal(e)
	}
}
func seed(t *testing.T, ctx context.Context, p *pgxpool.Pool) {
	exec(t, ctx, p, `INSERT INTO organizations(id,legal_name,business_type,created_at) VALUES($1,'Empresa','RESTAURANT',NOW()-INTERVAL '20 days')`, org)
	exec(t, ctx, p, `INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','admin@invalid.test','Admin','USER'),('chef','chef@invalid.test','Chef','USER'),('worker','worker@invalid.test','Worker','USER'),('super','super@invalid.test','Super','SUPER_ADMIN')`)
	exec(t, ctx, p, `INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'chef','CHEF'),($1,'worker','WORKER')`, org)
	exec(t, ctx, p, `INSERT INTO locations(id,organization_id,name,address)VALUES($1,$2,'Principal','Dirección')`, site, org)
	exec(t, ctx, p, `INSERT INTO membership_locations(organization_id,user_id,location_id)VALUES($1,'chef',$2),($1,'worker',$2)`, org, site)
}
func TestRuleIdempotencyVersionAndScheduledPrivacy(t *testing.T) {
	ctx, p := database(t)
	seed(t, ctx, p)
	s := application.New(New(p))
	v := domain.Rule{ID: uid(1), Name: "Sin actividad", Type: "ORGANIZATION_INACTIVE", Configuration: json.RawMessage(`{"inactivityDays":2}`), Active: true, EmailEnabled: true}
	if _, e := s.WriteRule(ctx, "worker", org, "denied-key", v, true); !errors.Is(e, domain.ErrDenied) {
		t.Fatal(e)
	}
	r, e := s.WriteRule(ctx, "admin", org, "create-key", v, true)
	if e != nil || r.RowVersion != 1 {
		t.Fatal(r, e)
	}
	replay, e := s.WriteRule(ctx, "admin", org, "create-key", v, true)
	if e != nil || replay.RowVersion != 1 {
		t.Fatal(replay, e)
	}
	changed := v
	changed.Name = "Otro cuerpo"
	if _, e = s.WriteRule(ctx, "admin", org, "create-key", changed, true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("idempotency accepted changed body", e)
	}
	if _, e = s.WriteRule(ctx, "super", org, "create-key", v, true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("idempotency crossed actor", e)
	}

	v.RowVersion = 1
	v.Name = "Sin operación"
	r, e = s.WriteRule(ctx, "admin", org, "update-key", v, false)
	if e != nil || r.RowVersion != 2 {
		t.Fatal(r, e)
	}
	if _, e = s.WriteRule(ctx, "admin", org, "stale-key", v, false); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	for range 2 {
		if e = s.EvaluateScheduled(ctx, time.Now().UTC()); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.Inbox(ctx, "admin", org, "ALL", pagination.Request{})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	a := page.Items[0]
	if !a.SourceAvailable || a.Status != "PENDING" {
		t.Fatal(a)
	}
	if _, e = s.Detail(ctx, "chef", org, a.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cross user", e)
	}
	if _, e = s.Inbox(ctx, "super", org, "ALL", pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("super without membership", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Transition(ctx, "admin", org, a.ID, true); results <- err }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	a, e = s.Transition(ctx, "admin", org, a.ID, false)
	if e != nil || a.Status != "ATTENDED" || a.ViewedAt == nil || a.AttendedAt == nil {
		t.Fatal(a, e)
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='alerts.attend'`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate audit", n)
	}
	p.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='notifications.alert.ready.v1'`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate outbox", n)
	}
	exec(t, ctx, p, `UPDATE organization_memberships SET role='WORKER' WHERE organization_id=$1 AND user_id='admin'`, org)
	page, e = s.Inbox(ctx, "admin", org, "ALL", pagination.Request{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("role revoked", page, e)
	}
}

func TestRequireSchemaRejectsMissingDedupConstraint(t *testing.T) {
	ctx, p := database(t)
	r := New(p)
	if e := r.RequireSchema(ctx); e != nil {
		t.Fatal(e)
	}
	exec(t, ctx, p, `ALTER TABLE alerts DROP CONSTRAINT alerts_dedup_uq`)
	if e := r.RequireSchema(ctx); e == nil {
		t.Fatal("accepted incomplete dedup schema")
	}
}
