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

	t.Run("evidencia de incidencia separada por etapa y acción", func(t *testing.T) {
		exec(`DELETE FROM membership_locations WHERE organization_id=$1 AND user_id='chef2' AND location_id=$2`, org, loc)
		exec(`UPDATE organization_memberships SET status='ACTIVE' WHERE organization_id=$1 AND user_id='chef'`, org)
		catalog := "91000000-0000-0000-0000-000000000001"
		incident := "92000000-0000-0000-0000-000000000001"
		action := "93000000-0000-0000-0000-000000000001"
		exec(`INSERT INTO incident_catalog(id,organization_id,code,name,area,normative_criterion,risk_level) VALUES($1,$2,'TEST','Criterio de prueba','Cocina','Referencia ficticia','HIGH')`, catalog, org)
		exec(`INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,description,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'worker','Descripción ficticia',$1,NOW())`, incident, org, loc, catalog)
		before := domain.UploadInput{ID: "94000000-0000-0000-0000-000000000001", IncidentID: incident, Stage: "BEFORE", OriginalName: "antes.png", MIMEType: "image/png", SizeBytes: 20, SHA256: strings.Repeat("a", 64)}
		if _, e := files.Reserve(ctx, "worker", org, before); e != nil {
			t.Fatal("foto inicial", e)
		}
		if _, e := files.Confirm(ctx, "worker", org, before.ID); e != nil {
			t.Fatal("confirmar inicial", e)
		}
		for n := 2; n <= 4; n++ {
			id := fmt.Sprintf("95000000-0000-0000-0000-%012d", n)
			exec(`INSERT INTO media_files(id,organization_id,location_id,object_key,storage_url,original_name,mime_type,size_bytes,sha256,status,uploaded_at) VALUES($1,$2,$3,$4,$5,'legado.png','image/png',20,$6,'AVAILABLE',NOW())`, id, org, loc, "legacy/"+id, "r2://legacy/"+id, strings.Repeat("a", 64))
			exec(`INSERT INTO incident_media(organization_id,incident_id,media_file_id,stage) VALUES($1,$2,$3,'BEFORE')`, org, incident, id)
		}
		legacy, e := files.ListIncident(ctx, "worker", org, incident, "", "BEFORE")
		if e != nil || len(legacy.Files) != 4 || legacy.Policy.MaxFiles != 3 {
			t.Fatal("ocultó fotos legadas", legacy, e)
		}
		extra := before
		extra.ID = "94000000-0000-0000-0000-000000000099"
		if _, e := files.Reserve(ctx, "worker", org, extra); !errors.Is(e, domain.ErrLimit) {
			t.Fatal("relajó límite de alta", e)
		}

		if _, e := files.ListIncident(ctx, "chef2", org, incident, "", "BEFORE"); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("sede ajena", e)
		}
		if _, e := files.ListIncident(ctx, "outside", org, incident, "", "BEFORE"); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("tenant ajeno", e)
		}
		exec(`INSERT INTO corrective_actions(id,organization_id,incident_id,responsible_user_id,description,due_at,status) VALUES($1,$2,$3,'worker','Corregir',NOW()+INTERVAL '1 day','IN_PROGRESS')`, action, org, incident)
		exec(`UPDATE incidents SET status='IN_PROGRESS' WHERE organization_id=$1 AND id=$2`, org, incident)
		after := before
		after.ID = "94000000-0000-0000-0000-000000000002"
		after.Stage = "AFTER"
		after.ActionID = action
		after.OriginalName = "después.png"
		if _, e := files.Reserve(ctx, "admin", org, after); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("supervisor fabricó corrección", e)
		}
		if _, e := files.Reserve(ctx, "worker", org, after); e != nil {
			t.Fatal("foto de corrección", e)
		}
		if _, e := files.Confirm(ctx, "worker", org, after.ID); e != nil {
			t.Fatal(e)
		}
		list, e := files.ListIncident(ctx, "admin", org, incident, action, "AFTER")
		if e != nil || len(list.Files) != 1 || list.Files[0].ActionID != action || list.Files[0].IncidentID != incident || list.Files[0].RunID != "" {
			t.Fatal("mezcló destinos", list, e)
		}
		bad := after
		bad.ID = before.ID
		if _, e := files.Reserve(ctx, "worker", org, bad); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("reutilizó foto inicial como posterior", e)
		}
		exec(`UPDATE incidents SET status='CLOSED',closed_at=NOW() WHERE organization_id=$1 AND id=$2`, org, incident)
		if _, e := files.Remove(ctx, "worker", org, after.ID); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("retiró evidencia de cierre", e)
		}
		if _, e := files.Download(ctx, "admin", org, after.ID); e != nil {
			t.Fatal("historial de fotos inaccesible", e)
		}
		exec(`UPDATE checklist_runs SET status='DRAFT' WHERE organization_id=$1 AND id=$2`, org, run)
		exec(`INSERT INTO checklist_media(organization_id,checklist_run_id,checklist_item_id,media_file_id) VALUES($1,$2,$3,$4)`, org, run, item, after.ID)
		if _, e := files.Remove(ctx, "chef", org, after.ID); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("retiró evidencia cerrada mediante otro destino", e)
		}
		exec(`DELETE FROM checklist_media WHERE organization_id=$1 AND media_file_id=$2`, org, after.ID)

		exec(`UPDATE incidents SET reported_by='chef' WHERE organization_id=$1 AND id=$2`, org, incident)
		exec(`UPDATE corrective_actions SET status='CANCELLED' WHERE organization_id=$1 AND id=$2`, org, action)
		historical, e := files.ListIncident(ctx, "worker", org, incident, "", "BEFORE")
		if e != nil || len(historical.Files) != 4 {
			t.Fatal("responsable histórico perdió acceso", historical, e)
		}
		exec(`UPDATE media_files SET location_id=$3 WHERE organization_id=$1 AND id=$2`, org, after.ID, otherLoc)
		wrong, e := files.ListIncident(ctx, "worker", org, incident, action, "AFTER")
		if e != nil || len(wrong.Files) != 0 {
			t.Fatal("listó foto física de otra sede", wrong, e)
		}
		if _, e := files.Download(ctx, "worker", org, after.ID); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("descargó foto ajena", e)
		}

	})

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
