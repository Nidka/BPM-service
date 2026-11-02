package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	checkdb "github.com/Nidka/BPM-service/internal/features/checklists/adapters/postgres"
	checks "github.com/Nidka/BPM-service/internal/features/checklists/application"
	checkdomain "github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/features/media/application"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrivateEvidencePostgres(t *testing.T) {
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
	schema := pgx.Identifier{fmt.Sprintf("media_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"V1__baseline.sql", "V4__checklist_evidence_items.sql"} {
		if name == "V4__checklist_evidence_items.sql" && New(pool).RequireSchema(ctx) == nil {
			t.Fatal("arranca sin migración V4")
		}
		sql, e := os.ReadFile("../../../../../migraciones/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	if err := New(pool).RequireSchema(ctx); err != nil {
		t.Fatal("no reconoce V4", err)
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	org := "10000000-0000-0000-0000-000000000001"
	foreign := "10000000-0000-0000-0000-000000000002"
	loc := "20000000-0000-0000-0000-000000000001"
	otherLoc := "20000000-0000-0000-0000-000000000002"
	tpl := "30000000-0000-0000-0000-000000000001"
	version := "40000000-0000-0000-0000-000000000001"
	item := "50000000-0000-0000-0000-000000000001"
	run := "60000000-0000-0000-0000-000000000001"
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Org','RESTAURANT'),($2,'Foreign','RESTAURANT')`, org, foreign)
	exec(`INSERT INTO application_users(id,email,display_name,platform_role) VALUES('chef','chef@example.invalid','Chef','USER'),('chef2','chef2@example.invalid','Otro chef','USER'),('admin','admin@example.invalid','Admin','USER'),('worker','worker@example.invalid','Worker','USER'),('outside','outside@example.invalid','Outside','USER'),('super','super@example.invalid','Super','SUPER_ADMIN')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'chef','CHEF'),($1,'chef2','CHEF'),($1,'admin','ADMIN'),($1,'worker','WORKER'),($2,'outside','CHEF')`, org, foreign)
	exec(`INSERT INTO locations(id,organization_id,name,address) VALUES($1,$3,'Principal','Dirección'),($2,$3,'Segunda','Dirección')`, loc, otherLoc, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2),($1,'worker',$2),($1,'chef2',$3)`, org, loc, otherLoc)
	controls := checks.New(checkdb.New(pool))
	if _, err = controls.CreateTemplate(ctx, "admin", org, checkdomain.Template{ID: tpl, VersionID: version, Name: "Con foto", Area: "Cocina", Items: []checkdomain.Item{{ID: item, SectionName: "Limpieza", Label: "¿Está limpio?", ResponseType: "COMPLIANCE", DisplayOrder: 1, Required: true, EvidenceRequiredOnFailure: true}}}); err != nil {
		t.Fatal("plantilla con evidencia", err)
	}
	if _, err = controls.CreateRun(ctx, "chef", org, checkdomain.Run{ID: run, LocationID: loc, TemplateVersionID: version, ClientOperationID: "70000000-0000-0000-0000-000000000001", ClientCreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	saved, err := controls.SaveAnswers(ctx, "chef", org, run, 1, "save-photo-1", []checkdomain.Answer{{ItemID: item, ComplianceResult: "NON_COMPLIANT"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = controls.Submit(ctx, "chef", org, run, saved.RowVersion, "submit-no-photo"); err == nil {
		t.Fatal("finalizó sin foto obligatoria")
	}
	files := application.New(New(pool), testObjects{}, domain.Policy{MaxBytes: 1024, MaxFiles: 3, AllowedMIMEs: []string{"image/png"}})
	in := domain.UploadInput{ID: "80000000-0000-0000-0000-000000000001", RunID: run, ItemID: item, OriginalName: "foto.png", MIMEType: "image/png", SizeBytes: 20, SHA256: strings.Repeat("a", 64)}
	for _, user := range []string{"worker", "outside", "chef2", "admin", "super"} {
		if _, e := files.Reserve(ctx, user, org, in); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("carga no autorizada", user, e)
		}
	}
	reserved, err := files.Reserve(ctx, "chef", org, in)
	if err != nil || reserved.File.Status != "PENDING_UPLOAD" {
		t.Fatal(reserved, err)
	}
	if _, err = files.Reserve(ctx, "chef", org, in); err != nil {
		t.Fatal("reintento", err)
	}
	changed := in
	changed.OriginalName = "diferente.png"
	if _, err = files.Reserve(ctx, "chef", org, changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("redefinió reserva", err)
	}
	if _, err = files.Download(ctx, "admin", org, in.ID); !errors.Is(err, domain.ErrNotReady) {
		t.Fatal("descargó pendiente", err)
	}
	if _, err = files.Confirm(ctx, "chef", org, in.ID); err != nil {
		t.Fatal(err)
	}
	// Volver a guardar conserva la evidencia ligada a la pregunta inmutable.
	saved, err = controls.SaveAnswers(ctx, "chef", org, run, saved.RowVersion, "save-photo-2", []checkdomain.Answer{{ItemID: item, ComplianceResult: "NON_COMPLIANT", Observation: "Con foto"}})
	if err != nil {
		t.Fatal("evidencia rota por guardar", err)
	}
	if saved.EvidenceCounts[item] != 1 {
		t.Fatal("recuento no confirmado", saved.EvidenceCounts)
	}
	for _, user := range []string{"admin", "super", "chef"} {
		list, e := files.List(ctx, user, org, run, item)
		if e != nil || len(list.Files) != 1 {
			t.Fatal("lectura autorizada", user, list, e)
		}
		if _, e = files.Download(ctx, user, org, in.ID); e != nil {
			t.Fatal(user, e)
		}
	}
	for _, user := range []string{"worker", "outside", "chef2"} {
		if _, e := files.List(ctx, user, org, run, item); e == nil {
			t.Fatal("leyó sede ajena", user)
		}
	}
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef2',$2)`, org, loc)
	if _, err = files.Download(ctx, "chef2", org, in.ID); err != nil {
		t.Fatal("chef no pudo supervisar sede asignada", err)
	}
	// Altas concurrentes respetan el límite bajo el mismo bloqueo de la revisión.
	var wg sync.WaitGroup
	out := make(chan error, 3)
	for i := 2; i <= 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v := in
			v.ID = fmt.Sprintf("80000000-0000-0000-0000-%012d", n)
			_, e := files.Reserve(ctx, "chef", org, v)
			out <- e
		}(i)
	}
	wg.Wait()
	close(out)
	limits := 0
	for e := range out {
		if errors.Is(e, domain.ErrLimit) {
			limits++
		} else if e != nil {
			t.Fatal(e)
		}
	}
	if limits != 1 {
		t.Fatal("límite concurrente", limits)
	}
	if _, err = files.Remove(ctx, "chef", org, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = files.Download(ctx, "admin", org, in.ID); !errors.Is(err, domain.ErrNotReady) {
		t.Fatal("descarga eliminada", err)
	}
	if _, err = controls.Submit(ctx, "chef", org, run, saved.RowVersion, "submit-removed-photo"); err == nil {
		t.Fatal("foto retirada todavía habilita envío")
	}
	in.ID = "80000000-0000-0000-0000-000000000005"
	if _, err = files.Reserve(ctx, "chef", org, in); err != nil {
		t.Fatal(err)
	}
	if _, err = files.Confirm(ctx, "chef", org, in.ID); err != nil {
		t.Fatal(err)
	}
	submitted, err := controls.Submit(ctx, "chef", org, run, saved.RowVersion, "submit-with-photo")
	if err != nil || submitted.Status != "SUBMITTED" {
		t.Fatal("finalización con evidencia", err)
	}
	if _, err = files.Remove(ctx, "chef", org, in.ID); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("eliminó evidencia histórica", err)
	}
	exec(`UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='chef'`, org)
	if _, err = files.Download(ctx, "chef", org, in.ID); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("membresía revocada", err)
	}
	var reservations int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='media.upload.reserved'`, org).Scan(&reservations); err != nil || reservations != 4 {
		t.Fatal("auditoría duplicada", reservations, err)
	}
}

type testObjects struct{}

func (testObjects) PresignUpload(context.Context, contracts.UploadRequest) (contracts.SignedRequest, error) {
	return contracts.SignedRequest{URL: "https://test.r2.cloudflarestorage.com/private", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (testObjects) PresignDownload(context.Context, string) (contracts.SignedRequest, error) {
	return contracts.SignedRequest{URL: "https://test.r2.cloudflarestorage.com/private", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (testObjects) Head(context.Context, string) (contracts.ObjectMetadata, error) {
	return contracts.ObjectMetadata{}, nil
}
func (testObjects) Inspect(context.Context, string) (contracts.ObjectMetadata, error) {
	return contracts.ObjectMetadata{ContentType: "image/png", ContentLength: 20, SHA256Hex: strings.Repeat("a", 64)}, nil
}
