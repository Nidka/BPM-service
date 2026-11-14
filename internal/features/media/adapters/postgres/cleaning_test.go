package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/media/application"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCleaningEvidencePostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("media_cleaning_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"V1__baseline.sql", "V4__checklist_evidence_items.sql", "V13__operations_shifts_stock.sql"} {
		sql, e := os.ReadFile("../../../../../migraciones/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	org, foreign := "10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000002"
	loc, otherLoc := "20000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"
	plan, execution, oldExecution := "30000000-0000-0000-0000-000000000001", "40000000-0000-0000-0000-000000000001", "40000000-0000-0000-0000-000000000002"
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Org','RESTAURANT'),($2,'Foreign','RESTAURANT')`, org, foreign)
	exec(`INSERT INTO application_users(id,email,display_name) VALUES('chef','chef@example.invalid','Chef'),('chef2','chef2@example.invalid','Otro chef'),('admin','admin@example.invalid','Admin'),('worker','worker@example.invalid','Worker'),('worker2','worker2@example.invalid','Otro worker'),('outside','outside@example.invalid','Ajeno')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'chef','CHEF'),($1,'chef2','CHEF'),($1,'admin','ADMIN'),($1,'worker','WORKER'),($1,'worker2','WORKER'),($2,'outside','CHEF')`, org, foreign)
	exec(`INSERT INTO locations(id,organization_id,name,address) VALUES($1,$3,'Principal','Dirección'),($2,$3,'Segunda','Dirección')`, loc, otherLoc, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2),($1,'worker',$2),($1,'worker2',$2),($1,'chef2',$3)`, org, loc, otherLoc)
	exec(`INSERT INTO cleaning_plans(id,organization_id,location_id,area,task,frequency) VALUES($1,$2,$3,'Cocina','Limpiar','DAILY')`, plan, org, loc)
	exec(`INSERT INTO cleaning_executions(id,organization_id,location_id,cleaning_plan_id,responsible_user_id,executed_at,result,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'worker',NOW(),'COMPLETED',$5,NOW())`, execution, org, loc, plan, "50000000-0000-0000-0000-000000000001")
	exec(`INSERT INTO cleaning_executions(id,organization_id,location_id,cleaning_plan_id,responsible_user_id,executed_at,result,client_operation_id,client_created_at,created_at) VALUES($1,$2,$3,$4,'worker',NOW(),'COMPLETED',$5,NOW(),NOW()-INTERVAL '3 days')`, oldExecution, org, loc, plan, "50000000-0000-0000-0000-000000000002")

	files := application.New(New(pool), testObjects{}, domain.Policy{MaxBytes: 1024, MaxFiles: 3, AllowedMIMEs: []string{"image/png"}})
	input := func(n int, target string) domain.UploadInput {
		return domain.UploadInput{ID: fmt.Sprintf("80000000-0000-0000-0000-%012d", n), CleaningID: target, OriginalName: "limpieza.png", MIMEType: "image/png", SizeBytes: 20, SHA256: strings.Repeat("a", 64)}
	}
	for _, user := range []string{"worker2", "chef", "admin", "chef2", "outside"} {
		if _, e := files.Reserve(ctx, user, org, input(1, execution)); e == nil {
			t.Fatal("cargó sin ser el responsable", user)
		}
	}
	if _, e := files.Reserve(ctx, "worker", foreign, input(1, execution)); e == nil {
		t.Fatal("cargó con tenant ajeno")
	}
	if _, e := files.Reserve(ctx, "worker", org, input(1, oldExecution)); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("cargó fuera de la ventana de edición", e)
	}
	mixed := input(1, execution)
	mixed.RunID, mixed.ItemID = execution, execution
	var invalid *domain.ValidationError
	if _, e := files.Reserve(ctx, "worker", org, mixed); !errors.As(e, &invalid) {
		t.Fatal("mezcló destinos", e)
	}
	pdf := input(1, execution)
	pdf.MIMEType = "application/pdf"
	if _, e := files.Reserve(ctx, "worker", org, pdf); !errors.As(e, &invalid) {
		t.Fatal("aceptó PDF", e)
	}
	reserved, err := files.Reserve(ctx, "worker", org, input(1, execution))
	if err != nil || reserved.File.Status != "PENDING_UPLOAD" || reserved.File.CleaningID != execution || reserved.Upload == nil {
		t.Fatal(reserved, err)
	}
	if again, e := files.Reserve(ctx, "worker", org, input(1, execution)); e != nil || again.File.ID != reserved.File.ID {
		t.Fatal("reintento de reserva", e)
	}
	changed := input(1, execution)
	changed.OriginalName = "otra.png"
	if _, e := files.Reserve(ctx, "worker", org, changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("redefinió la reserva", e)
	}
	if _, e := files.Download(ctx, "admin", org, reserved.File.ID); !errors.Is(e, domain.ErrNotReady) {
		t.Fatal("descargó pendiente", e)
	}
	if _, e := files.Confirm(ctx, "worker2", org, reserved.File.ID); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("otro worker confirmó", e)
	}
	if f, e := files.Confirm(ctx, "worker", org, reserved.File.ID); e != nil || f.Status != "AVAILABLE" {
		t.Fatal(f, e)
	}
	for _, user := range []string{"worker", "chef", "admin", "super"} {
		list, e := files.ListCleaning(ctx, user, org, execution)
		if user == "super" {
			if e == nil {
				t.Fatal("un usuario sin membresía leyó")
			}
			continue
		}
		if e != nil || len(list.Files) != 1 || list.Files[0].CleaningID != execution {
			t.Fatal("lectura autorizada", user, list, e)
		}
		if _, e = files.Download(ctx, user, org, reserved.File.ID); e != nil {
			t.Fatal("descarga", user, e)
		}
	}
	for _, user := range []string{"worker2", "chef2", "outside"} {
		if _, e := files.ListCleaning(ctx, user, org, execution); e == nil {
			t.Fatal("leyó sin permiso", user)
		}
		if _, e := files.Download(ctx, user, org, reserved.File.ID); e == nil {
			t.Fatal("descargó sin permiso", user)
		}
	}
	for n := 2; n <= 3; n++ {
		if _, e := files.Reserve(ctx, "worker", org, input(n, execution)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := files.Reserve(ctx, "worker", org, input(4, execution)); !errors.Is(e, domain.ErrLimit) {
		t.Fatal("límite de tres fotos", e)
	}
	if _, e := files.Remove(ctx, "admin", org, reserved.File.ID); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("el administrador retiró", e)
	}
	if f, e := files.Remove(ctx, "worker", org, reserved.File.ID); e != nil || f.Status != "DELETED" {
		t.Fatal(f, e)
	}
	if _, e := files.Reserve(ctx, "worker", org, input(4, execution)); e != nil {
		t.Fatal("tras retirar puede cargar otra", e)
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND entity_type='media_file'`, org).Scan(&audits)
	if audits < 6 {
		t.Fatal("auditoría", audits)
	}
}
