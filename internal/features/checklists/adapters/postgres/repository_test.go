package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/checklists/application"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Se ejecuta únicamente contra bpm_test y en un esquema temporal aislado.
func TestChecklistPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires database bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("checklist_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	baseline, err := os.ReadFile("../../../../../migraciones/V1__baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(baseline)); err != nil {
		t.Fatal(err)
	}
	mediaMigration, err := os.ReadFile("../../../../../migraciones/V4__checklist_evidence_items.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(mediaMigration)); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	org := "10000000-0000-0000-0000-000000000001"
	other := "10000000-0000-0000-0000-000000000002"
	loc := "20000000-0000-0000-0000-000000000001"
	templateID := "30000000-0000-0000-0000-000000000001"
	versionID := "40000000-0000-0000-0000-000000000001"
	itemID := "50000000-0000-0000-0000-000000000001"
	runID := "60000000-0000-0000-0000-000000000001"
	operationID := "70000000-0000-0000-0000-000000000001"
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Uno','RESTAURANT'),($2,'Dos','RESTAURANT')`, org, other)
	exec(`INSERT INTO application_users(id,email,display_name) VALUES('admin','admin@example.invalid','Admin'),('worker','worker@example.invalid','Worker'),('chef','chef@example.invalid','Chef'),('super','super@example.invalid','Super'),('outsider','outsider@example.invalid','Outsider')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'worker','WORKER'),($1,'chef','CHEF'),($2,'outsider','WORKER')`, org, other)
	exec(`INSERT INTO locations(id,organization_id,name,address) VALUES($1,$2,'Cocina','Av. Prueba 123')`, loc, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'worker',$2)`, org, loc)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2)`, org, loc)
	exec(`UPDATE application_users SET platform_role='SUPER_ADMIN' WHERE id='super'`)
	service := application.New(New(pool))
	tpl := domain.Template{ID: templateID, VersionID: versionID, Name: "Higiene diaria", Area: "Cocina",
		Items: []domain.Item{{ID: itemID, SectionName: "Personal", Label: "Lavado de manos", ResponseType: "COMPLIANCE", DisplayOrder: 1, Required: true}}}
	if _, err = service.CreateTemplate(ctx, "worker", org, tpl); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("worker published", err)
	}
	if _, err = service.CreateTemplate(ctx, "admin", org, tpl); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Catalog(ctx, "outsider", org); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("cross tenant catalog", err)
	}
	catalog, err := service.Catalog(ctx, "worker", org)
	if err != nil || len(catalog) != 1 || len(catalog[0].Items) != 1 {
		t.Fatal("catalog failed", err, catalog)
	}
	input := domain.Run{ID: runID, LocationID: loc, TemplateVersionID: versionID, ClientOperationID: operationID, ClientCreatedAt: time.Now().UTC().Add(-time.Minute)}
	if _, err = service.CreateRun(ctx, "outsider", org, input); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("outsider started run", err)
	}
	created, err := service.CreateRun(ctx, "chef", org, input)
	if err != nil || created.RowVersion != 1 {
		t.Fatal("create run", err)
	}
	if _, err = service.CreateRun(ctx, "chef", org, input); err != nil {
		t.Fatal("replay create run", err)
	}
	answers := []domain.Answer{{ItemID: itemID, ComplianceResult: "COMPLIANT"}}
	saved, err := service.SaveAnswers(ctx, "chef", org, runID, 1, "save-0001", answers)
	if err != nil || saved.RowVersion != 2 || len(saved.Answers) != 1 {
		t.Fatal("save answers", err)
	}
	replayed, err := service.SaveAnswers(ctx, "chef", org, runID, 1, "save-0001", answers)
	if err != nil || replayed.RowVersion != 2 {
		t.Fatal("idempotent save", err)
	}
	if _, err = service.SaveAnswers(ctx, "chef", org, runID, 1, "save-0002", answers); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatal("stale save", err)
	}
	if _, err = service.Submit(ctx, "outsider", org, runID, 2, "submit-0000"); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("outsider submitted", err)
	}
	submitted, err := service.Submit(ctx, "chef", org, runID, 2, "submit-0001")
	if err != nil || submitted.Status != "SUBMITTED" || submitted.CompliancePercent == nil || *submitted.CompliancePercent != 100 {
		t.Fatal("submit", err)
	}
	if _, err = service.SaveAnswers(ctx, "chef", org, runID, 3, "save-0003", answers); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("edited submitted run", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1`, org).Scan(&count); err != nil || count != 4 {
		t.Fatal("audit count", err, count)
	}
	testRunHistory(t, ctx, pool, service, org, other, loc, versionID, itemID)
	testCatalogPagination(t, ctx, pool, service, org, other)
	testAssignments(t, ctx, pool, service, org, other, loc, templateID, versionID, itemID)

}
