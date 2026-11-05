package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/alerts/application"
	"github.com/Nidka/BPM-service/internal/features/alerts/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"testing"
	"time"
)

func rule(t *testing.T, ctx context.Context, p *pgxpool.Pool, n int, kind, config string, location *string) domain.Rule {
	t.Helper()
	v, e := application.New(New(p)).WriteRule(ctx, "admin", org, "rule-key-"+uid(n), domain.Rule{ID: uid(n), Name: "Regla de prueba", Type: kind, LocationID: location, Configuration: json.RawMessage(config), Active: true, EmailEnabled: true, PushEnabled: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestReceiptDangerBoundarySnapshotDedupAndRevocation(t *testing.T) {
	ctx, p := database(t)
	seed(t, ctx, p)
	r := New(p)
	s := application.New(r)
	v := rule(t, ctx, p, 1, "TEMPERATURE_DANGER", `{"minimumTemperatureCelsius":4,"maximumTemperatureCelsius":60}`, nil)
	exec(t, ctx, p, `INSERT INTO suppliers(id,organization_id,name)VALUES($1,$2,'Proveedor')`, uid(10), org)
	exec(t, ctx, p, `INSERT INTO products(id,organization_id,name,unit)VALUES($1,$2,'Producto','KG')`, uid(11), org)
	exec(t, ctx, p, `INSERT INTO supply_receipts(id,organization_id,location_id,supplier_id,received_by,received_at,client_operation_id,client_created_at)VALUES($1,$2,$3,$4,'worker',NOW(),$5,NOW())`, uid(12), org, site, uid(10), uid(13))
	for i, temp := range []any{4, 60, 3.99, 60.01, nil} {
		exec(t, ctx, p, `INSERT INTO supply_receipt_items(id,organization_id,receipt_id,product_id,quantity,temperature_celsius,decision)VALUES($1,$2,$3,$4,1,$5,'ACCEPTED')`, uid(20+i), org, uid(12), uid(11), temp)
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.EvaluateReceipt(ctx, org, uid(12), time.Now().UTC()) }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.Inbox(ctx, "chef", org, "ALL", pagination.Request{Size: 1})
	if e != nil || len(page.Items) != 1 || page.NextToken == "" || page.Items[0].SourceParentID == nil || *page.Items[0].SourceParentID != uid(12) {
		t.Fatal(page, e)
	}
	if _, e = s.Inbox(ctx, "admin", org, "ALL", pagination.Request{Token: page.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
		t.Fatal("cursor actor", e)
	}
	count, e := s.Count(ctx, "chef", org)
	if e != nil || count != 2 {
		t.Fatal(count, e)
	}
	count, e = s.Count(ctx, "worker", org)
	if e != nil || count != 0 {
		t.Fatal(count, e)
	}
	var alerts, outbox int
	p.QueryRow(ctx, `SELECT count(*) FROM alerts`).Scan(&alerts)
	p.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&outbox)
	if alerts != 4 || outbox != 4 {
		t.Fatal(alerts, outbox)
	}
	v.Name = "Criterio nuevo"
	v.Configuration = json.RawMessage(`{"minimumTemperatureCelsius":70,"maximumTemperatureCelsius":80}`)
	v.Active = false
	if _, e = s.WriteRule(ctx, "admin", org, "update-criterion", v, false); e != nil {
		t.Fatal(e)
	}
	var snapshot []byte
	p.QueryRow(ctx, `SELECT context_snapshot->'configuration' FROM alerts LIMIT 1`).Scan(&snapshot)
	var cfg map[string]float64
	json.Unmarshal(snapshot, &cfg)
	if cfg["minimumTemperatureCelsius"] != 4 {
		t.Fatal("rewrote snapshot", string(snapshot))
	}
	count, e = s.Count(ctx, "chef", org)
	if e != nil || count != 2 {
		t.Fatal("rule disable hides history", count, e)
	}
	exec(t, ctx, p, `DELETE FROM membership_locations WHERE organization_id=$1 AND user_id='chef'`, org)
	count, e = s.Count(ctx, "chef", org)
	if e != nil || count != 0 {
		t.Fatal("location revocation", count, e)
	}
	var authorized bool
	p.QueryRow(ctx, `SELECT alert_recipient_authorized($1,$2,'chef')`, org, page.Items[0].ID).Scan(&authorized)
	if authorized {
		t.Fatal("delivery authorized revoked sede")
	}
}
func TestScheduledChecklistDSTDraftAndCompletion(t *testing.T) {
	ctx, p := database(t)
	seed(t, ctx, p)
	s := application.New(New(p))
	exec(t, ctx, p, `UPDATE locations SET timezone='America/New_York' WHERE id=$1`, site)
	rule(t, ctx, p, 1, "CHECKLIST_MISSING", `{"afterLocalTime":"12:00"}`, nil)
	exec(t, ctx, p, `INSERT INTO checklist_templates(id,organization_id,name,area)VALUES($1,$2,'Control','Cocina')`, uid(10), org)
	exec(t, ctx, p, `INSERT INTO checklist_template_versions(id,organization_id,template_id,version_number,status,published_at)VALUES($1,$2,$3,1,'PUBLISHED',NOW())`, uid(11), org, uid(10))
	now := time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC)
	exec(t, ctx, p, `INSERT INTO checklist_assignments(id,organization_id,location_id,template_id,frequency,created_at)VALUES($1,$2,$3,$4,'DAILY',$5),($6,$2,$3,$4,'WEEKLY',$5),($7,$2,$3,$4,'DAILY',$8)`, uid(12), org, site, uid(10), now.Add(-24*time.Hour), uid(13), uid(14), now.Add(48*time.Hour))
	exec(t, ctx, p, `INSERT INTO checklist_runs(id,organization_id,location_id,template_version_id,assignment_id,performed_by,client_operation_id,client_created_at,created_at)VALUES($1,$2,$3,$4,$5,'chef',$6,$7,$7)`, uid(20), org, site, uid(11), uid(12), uid(21), now.Add(-time.Hour))
	if e := s.EvaluateScheduled(ctx, now.Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	n, e := s.Count(ctx, "chef", org)
	if e != nil || n != 0 {
		t.Fatal("before deadline", n, e)
	}
	for range 2 {
		if e = s.EvaluateScheduled(ctx, now); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.Inbox(ctx, "chef", org, "ALL", pagination.Request{})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	var start, end time.Time
	if e = p.QueryRow(ctx, `SELECT(context_snapshot->>'periodStart')::timestamptz,(context_snapshot->>'periodEnd')::timestamptz FROM alerts`).Scan(&start, &end); e != nil || end.Sub(start) != 25*time.Hour {
		t.Fatal(start, end, e)
	}
	exec(t, ctx, p, `UPDATE checklist_runs SET status='SUBMITTED',submitted_at=$1 WHERE id=$2`, now, uid(20))
	var pending bool
	if e = p.QueryRow(ctx, `SELECT alert_source_pending($1,$2)`, org, page.Items[0].ID).Scan(&pending); e != nil || pending {
		t.Fatal("completed source pending", pending, e)
	}
	a, e := s.Detail(ctx, "chef", org, page.Items[0].ID)
	if e != nil || a.SourceAvailable {
		t.Fatal("history missing", a, e)
	}
	exec(t, ctx, p, `UPDATE organization_memberships SET role='WORKER' WHERE user_id='chef' AND organization_id=$1`, org)
	if _, e = s.Detail(ctx, "chef", org, a.ID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("revoked role", e)
	}
}
func TestTrainingConjunctiveTargetsAndServerActivityEpisode(t *testing.T) {
	ctx, p := database(t)
	seed(t, ctx, p)
	s := application.New(New(p))
	rule(t, ctx, p, 1, "TRAINING_ASSIGNED", `{}`, nil)
	exec(t, ctx, p, `INSERT INTO training_courses(id,organization_id,code,title,status)VALUES($1,$2,'COURSE','Curso','PUBLISHED')`, uid(10), org)
	exec(t, ctx, p, `INSERT INTO training_assignments(id,organization_id,course_id,location_id,target_user_id,target_role,created_at)VALUES($1,$2,$3,$4,'worker','CHEF',NOW()),($5,$2,$3,$4,'chef','CHEF',NOW()),($6,$2,$3,NULL,NULL,'WORKER',NOW()),($7,$2,$3,$4,'chef',NULL,NOW()-INTERVAL '1 day')`, uid(11), org, uid(10), site, uid(12), uid(13), uid(14))
	if e := s.EvaluateScheduled(ctx, time.Now().Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	for _, user := range []string{"chef", "worker"} {
		n, e := s.Count(ctx, user, org)
		if e != nil || n != 1 {
			t.Fatal(user, n, e)
		}
	}
	exec(t, ctx, p, `UPDATE training_assignments SET status='COMPLETED' WHERE id=$1`, uid(12))
	page, e := s.Inbox(ctx, "chef", org, "ALL", pagination.Request{})
	if e != nil || len(page.Items) != 1 || page.Items[0].SourceAvailable {
		t.Fatal(page, e)
	}
	rule(t, ctx, p, 2, "ORGANIZATION_INACTIVE", `{"inactivityDays":2}`, nil)
	now := time.Now().UTC()
	if e = s.EvaluateScheduled(ctx, now); e != nil {
		t.Fatal(e)
	}
	page, e = s.Inbox(ctx, "admin", org, "ALL", pagination.Request{})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	exec(t, ctx, p, `INSERT INTO cleaning_plans(id,organization_id,location_id,area,task,frequency)VALUES($1,$2,$3,'Cocina','Limpiar','DAILY')`, uid(20), org, site)
	exec(t, ctx, p, `INSERT INTO cleaning_executions(id,organization_id,location_id,cleaning_plan_id,responsible_user_id,executed_at,result,client_operation_id,client_created_at)VALUES($1,$2,$3,$4,'worker',$5,'COMPLETED',$6,$5)`, uid(21), org, site, uid(20), now.Add(300*time.Hour), uid(22))
	if e = s.EvaluateScheduled(ctx, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	n, e := s.Count(ctx, "admin", org)
	if e != nil || n != 1 {
		t.Fatal("future client activity ignored", n, e)
	}
	if e = s.EvaluateScheduled(ctx, now.Add(49*time.Hour)); e != nil {
		t.Fatal(e)
	}
	n, e = s.Count(ctx, "admin", org)
	if e != nil || n != 2 {
		t.Fatal("new inactivity episode", n, e)
	}
}

func TestReceiptSourceRechecksChangedMeasurement(t *testing.T) {
	ctx, p := database(t)
	seed(t, ctx, p)
	s := application.New(New(p))
	rule(t, ctx, p, 1, "TEMPERATURE_DANGER", `{"minimumTemperatureCelsius":4,"maximumTemperatureCelsius":60}`, nil)
	exec(t, ctx, p, `INSERT INTO suppliers(id,organization_id,name)VALUES($1,$2,'Proveedor')`, uid(10), org)
	exec(t, ctx, p, `INSERT INTO products(id,organization_id,name,unit)VALUES($1,$2,'Producto','KG')`, uid(11), org)
	exec(t, ctx, p, `INSERT INTO supply_receipts(id,organization_id,location_id,supplier_id,received_by,received_at,client_operation_id,client_created_at)VALUES($1,$2,$3,$4,'worker',NOW(),$5,NOW())`, uid(12), org, site, uid(10), uid(13))
	exec(t, ctx, p, `INSERT INTO supply_receipt_items(id,organization_id,receipt_id,product_id,quantity,temperature_celsius,decision)VALUES($1,$2,$3,$4,1,20,'ACCEPTED')`, uid(20), org, uid(12), uid(11))
	if e := s.EvaluateReceipt(ctx, org, uid(12), time.Now()); e != nil {
		t.Fatal(e)
	}
	exec(t, ctx, p, `UPDATE supply_receipt_items SET temperature_celsius=2 WHERE id=$1`, uid(20))
	var pending bool
	if e := p.QueryRow(ctx, `SELECT bool_or(alert_source_pending(organization_id,id)) FROM alerts`).Scan(&pending); e != nil || pending {
		t.Fatal("changed measurement remains deliverable", pending, e)
	}
}
