package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/checklists/application"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Con V24, un control enviado con «No cumple» u observación avisa (con push) al ADMIN y al CHEF de la sede, no a quien lo hizo (aquí el jefe).
func TestControlConNoCumpleAvisaAlJefe(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires database bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("checklist_flag_%d", time.Now().UnixNano())}.Sanitize()
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
	for _, n := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql", "V7__alerts_notifications.sql", "V13__operations_shifts_stock.sql", "V14__alerts_admin_operates.sql", "V21__alerts_incident_assigned.sql", "V23__alerts_incident_reported.sql", "V24__staff_messages_control_flagged_test_push.sql"} {
		b, e := os.ReadFile("../../../../../migraciones/" + n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(b)); e != nil {
			t.Fatal(n, e)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	org, loc := "10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000001"
	versionID, itemID, runID := "40000000-0000-0000-0000-000000000001", "50000000-0000-0000-0000-000000000001", "60000000-0000-0000-0000-000000000001"
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Uno','RESTAURANT')`, org)
	exec(`INSERT INTO application_users(id,email,display_name) VALUES('admin','admin@example.invalid','Admin'),('worker','worker@example.invalid','Worker'),('chef','chef@example.invalid','Chef')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'worker','WORKER'),($1,'chef','CHEF')`, org)
	exec(`INSERT INTO locations(id,organization_id,name,address) VALUES($1,$2,'Cocina','Av. Prueba 123')`, loc, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'worker',$2),($1,'chef',$2)`, org, loc)
	service := application.New(New(pool))
	tpl := domain.Template{ID: "30000000-0000-0000-0000-000000000001", VersionID: versionID, Name: "Higiene diaria", Area: "Cocina",
		Items: []domain.Item{{ID: itemID, SectionName: "Personal", Label: "Lavado de manos", ResponseType: "COMPLIANCE", DisplayOrder: 1, Required: true}}}
	if _, err = service.CreateTemplate(ctx, "admin", org, tpl); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateRun(ctx, "chef", org, domain.Run{ID: runID, LocationID: loc, TemplateVersionID: versionID, ClientOperationID: "70000000-0000-0000-0000-000000000001", ClientCreatedAt: time.Now().UTC().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveAnswers(ctx, "chef", org, runID, 1, "save-0001", []domain.Answer{{ItemID: itemID, ComplianceResult: "NON_COMPLIANT", Observation: "No hay jabón"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(ctx, "chef", org, runID, 2, "submit-0001"); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT recipient_user_id,title,message,alert_recipient_authorized($1,id,recipient_user_id) FROM alerts WHERE alert_type='CONTROL_FLAGGED' ORDER BY recipient_user_id`, org)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var user, title, message string
		var ok bool
		if err = rows.Scan(&user, &title, &message, &ok); err != nil {
			t.Fatal(err)
		}
		if title != "Control con «No cumple»: Higiene diaria" || message != "Cocina · 1 no cumple · 1 observación" || !ok {
			t.Fatal(user, title, message, ok)
		}
		got = append(got, user)
	}
	if len(got) != 1 || got[0] != "admin" { // quien hizo el control (chef) no se avisa a sí mismo
		t.Fatal("destinatarios", got)
	}
}
