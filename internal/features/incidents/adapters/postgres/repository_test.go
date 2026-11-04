package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/features/incidents/application"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
	"time"
)

const org = "10000000-0000-0000-0000-000000000001"
const foreign = "10000000-0000-0000-0000-000000000002"
const site = "20000000-0000-0000-0000-000000000001"
const otherSite = "20000000-0000-0000-0000-000000000002"
const catalogID = "30000000-0000-0000-0000-000000000001"

func uid(kind, n int) string { return fmt.Sprintf("%08d-0000-0000-0000-%012d", kind, n) }
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
	schema := pgx.Identifier{fmt.Sprintf("incidents_%d", time.Now().UnixNano())}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 8
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql"} {
		if name == "V6__incidents.sql" && New(pool).RequireSchema(ctx) == nil {
			t.Fatal("operaciones arrancan sin V6")
		}
		sql, e := os.ReadFile("../../../../../migraciones/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	if e = New(pool).RequireSchema(ctx); e != nil {
		t.Fatal("no reconoce V6", e)
	}
	return ctx, pool
}
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Organización','RESTAURANT'),($2,'Ajena','RESTAURANT')`, org, foreign)
	exec(`INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','admin@example.invalid','Admin','USER'),('chef','chef@example.invalid','Chef','USER'),('chef2','chef2@example.invalid','Otro chef','USER'),('worker','worker@example.invalid','Worker','USER'),('worker2','worker2@example.invalid','Otro worker','USER'),('outside','outside@example.invalid','Ajeno','USER'),('super','super@example.invalid','Super','SUPER_ADMIN')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'chef','CHEF'),($1,'chef2','CHEF'),($1,'worker','WORKER'),($1,'worker2','WORKER'),($2,'outside','CHEF')`, org, foreign)
	exec(`INSERT INTO locations(id,organization_id,name,address,timezone) VALUES($1,$3,'Principal','Dirección','America/Lima'),($2,$3,'Otra','Dirección','America/Lima')`, site, otherSite, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2),($1,'worker',$2),($1,'worker2',$2),($1,'chef2',$3)`, org, site, otherSite)
}
func TestIncidentPostgresLifecyclePrivacyAndRecovery(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	c := domain.Catalog{ID: catalogID, Code: "TEST", Name: "Superficie", Area: "Cocina", NormativeCriterion: "Criterio original", RiskLevel: "HIGH", EvidenceRequired: true}
	if _, e := s.CreateCatalog(ctx, "admin", org, "catalog-key", c); e != nil {
		t.Fatal(e)
	}
	r := domain.Report{ID: uid(4, 1), LocationID: site, CatalogID: catalogID, Description: "Superficie dañada", ClientOperationID: uid(5, 1), ClientCreatedAt: now}
	v, e := s.Report(ctx, "worker", org, "report-key", r)
	if e != nil {
		t.Fatal(e)
	}
	if v.Status != "OPEN" || v.RowVersion != 1 {
		t.Fatal(v)
	}
	original := v
	for _, u := range []string{"worker2", "chef2", "outside"} {
		if _, e = s.Detail(ctx, u, org, v.ID); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("leyó ajeno", u, e)
		}
	}
	p, e := s.Incidents(ctx, "worker2", org, site, "", pagination.Request{})
	if e != nil || len(p.Items) != 0 {
		t.Fatal("filtró worker", p, e)
	}
	version := domain.CatalogVersion{Catalog: c, ExpectedVersion: 1}
	version.ID = uid(3, 2)
	version.NormativeCriterion = "Criterio posterior"
	version.EvidenceRequired = false
	if _, e = s.VersionCatalog(ctx, "admin", org, catalogID, "catalog-v2", version); e != nil {
		t.Fatal(e)
	}
	if v, e = s.Detail(ctx, "worker", org, v.ID); e != nil || v.NormativeCriterion != "Criterio original" || !v.EvidenceRequired || v.CatalogVersion != 1 {
		t.Fatal(v, e)
	}
	assign := domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker2", Description: "Reparar", DueAt: now.Add(time.Hour)}
	bad := assign
	bad.ResponsibleUserID = "chef2"
	if _, e = s.Act(ctx, "chef", org, v.ID, "bad-site-key", bad); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("asignó sede ajena", e)
	}
	bad.ResponsibleUserID = "admin"
	if _, e = s.Act(ctx, "chef", org, v.ID, "bad-admin-key", bad); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("asignó admin", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE application_users SET status='INACTIVE' WHERE id='worker2'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Act(ctx, "chef", org, v.ID, "inactive-key", assign); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("asignó inactivo", e)
	}
	pool.Exec(ctx, `UPDATE application_users SET status='ACTIVE' WHERE id='worker2'`)
	v, e = s.Act(ctx, "chef", org, v.ID, "assign-key", assign)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Act(ctx, "worker", org, v.ID, "not-owner-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("otro inició", e)
	}
	v, e = s.Act(ctx, "worker2", org, v.ID, "start-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"})
	if e != nil {
		t.Fatal(e)
	}
	resolve := domain.Command{OperationID: uid(6, 3), RowVersion: 3, ActionType: "RESOLVE", Note: "Superficie reparada"}
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for i := range 3 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.Act(ctx, "worker2", org, r.ID, fmt.Sprintf("resolve-key-%d", i), resolve)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	for e = range results {
		if e != nil {
			t.Fatal("replay concurrente", e)
		}
	}
	verify := domain.Command{OperationID: uid(6, 4), RowVersion: 4, ActionType: "VERIFY", Note: "Conforme"}
	if _, e = s.Act(ctx, "chef", org, v.ID, "verify-empty", verify); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("cerró sin evidencia", e)
	}
	evidence := func(n int, stage, status string, action any) {
		t.Helper()
		_, e := pool.Exec(ctx, `INSERT INTO media_files(id,organization_id,location_id,object_key,storage_url,original_name,mime_type,size_bytes,status,created_by) VALUES($1,$2,$3,$4::text,$4::text,'foto.jpg','image/jpeg',32,$5,'worker2')`, uid(8, n), org, site, fmt.Sprintf("test/%d", n), status)
		if e != nil {
			t.Fatal(e)
		}
		_, e = pool.Exec(ctx, `INSERT INTO incident_media(organization_id,incident_id,corrective_action_id,media_file_id,stage) VALUES($1,$2,$3,$4,$5)`, org, r.ID, action, uid(8, n), stage)
		if e != nil {
			t.Fatal(e)
		}
	}
	evidence(1, "BEFORE", "AVAILABLE", nil)
	evidence(2, "AFTER", "PENDING_UPLOAD", assign.ActionID)
	if _, e = s.Act(ctx, "chef", org, v.ID, "verify-pending", verify); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("pendiente autorizó cierre", e)
	}
	pool.Exec(ctx, `INSERT INTO corrective_actions(id,organization_id,incident_id,responsible_user_id,description,due_at) VALUES($1,$2,$3,'worker','Legado',NOW())`, uid(7, 9), org, r.ID)
	evidence(3, "AFTER", "AVAILABLE", uid(7, 9))
	if _, e = s.Act(ctx, "chef", org, v.ID, "verify-other-action", verify); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("otra acción autorizó", e)
	}
	pool.Exec(ctx, `DELETE FROM incident_media WHERE media_file_id=$1`, uid(8, 3))
	pool.Exec(ctx, `DELETE FROM corrective_actions WHERE id=$1`, uid(7, 9))
	pool.Exec(ctx, `UPDATE media_files SET status='AVAILABLE',mime_type='application/pdf' WHERE id=$1`, uid(8, 2))
	if _, e = s.Act(ctx, "chef", org, v.ID, "verify-nonphoto", verify); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("archivo que no es foto autorizó cierre", e)
	}
	pool.Exec(ctx, `UPDATE media_files SET mime_type='image/jpeg' WHERE id=$1`, uid(8, 2))
	pool.Exec(ctx, `UPDATE media_files SET status='AVAILABLE' WHERE id=$1`, uid(8, 2))
	execSQL(t, ctx, pool, `INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'worker',$5,NOW())`, uid(4, 2), org, site, catalogID, uid(5, 2))
	execSQL(t, ctx, pool, `INSERT INTO incident_media(organization_id,incident_id,media_file_id,stage) VALUES($1,$2,$3,'BEFORE')`, org, uid(4, 2), uid(8, 2))
	if _, e = s.Act(ctx, "chef", org, v.ID, "verify-shared-photo", verify); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("foto de varios destinos autorizó cierre", e)
	}
	execSQL(t, ctx, pool, `DELETE FROM incident_media WHERE organization_id=$1 AND incident_id=$2`, org, uid(4, 2))
	v, e = s.Act(ctx, "chef", org, v.ID, "verify-key", verify)
	if e != nil || v.Status != "CLOSED" || v.RowVersion != 5 {
		t.Fatal(v, e)
	}
	for _, key := range []string{"report-key", "report-new-key"} {
		replay, e := s.Report(ctx, "worker", org, key, r)
		if e != nil || replay.Status != "OPEN" || replay.RowVersion != 1 || replay.CreatedAt != original.CreatedAt {
			t.Fatal("reporte perdió original", replay, e)
		}
	}
	replay, e := s.Act(ctx, "chef", org, v.ID, "assign-new-key", assign)
	if e != nil || replay.Status != "ASSIGNED" || replay.RowVersion != 2 {
		t.Fatal("acción perdió original", replay, e)
	}
	changed := assign
	changed.Description = "Otro cuerpo"
	if _, e = s.Act(ctx, "chef", org, v.ID, "changed-key", changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("operation cambió cuerpo", e)
	}
	history, e := s.History(ctx, "worker2", org, v.ID, pagination.Request{Size: 2})
	if e != nil || len(history.Items) != 2 || history.NextToken == "" {
		t.Fatal(history, e)
	}
	if _, e = s.History(ctx, "chef", org, v.ID, pagination.Request{Token: history.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
		t.Fatal("cursor cruzó actor", e)
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM incident_events WHERE organization_id=$1`, org).Scan(&count)
	if count != 5 {
		t.Fatal("duplicó eventos", count)
	}
	if _, e = pool.Exec(ctx, `UPDATE incident_events SET note='sobrescribir' WHERE incident_id=$1`, v.ID); e == nil {
		t.Fatal("evento mutable")
	}
	if _, e = pool.Exec(ctx, `UPDATE incident_catalog SET normative_criterion='sobrescribir' WHERE id=$1`, catalogID); e == nil {
		t.Fatal("catálogo mutable")
	}
	pool.Exec(ctx, `UPDATE application_users SET status='INACTIVE' WHERE id='worker'`)
	if _, e = s.Report(ctx, "worker", org, "report-key", r); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("replay sin reautorizar", e)
	}
}
