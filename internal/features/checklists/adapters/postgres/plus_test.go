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
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/Nidka/BPM-service/internal/shared/domain/shiftwindow"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func plusDatabase(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires database bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("checklist_plus_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"V1__baseline.sql", "V4__checklist_evidence_items.sql", "V13__operations_shifts_stock.sql"} {
		sql, e := os.ReadFile("../../../../../migraciones/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	return ctx, pool
}

func TestShiftAssignmentsAndTemplateVersionsPostgres(t *testing.T) {
	ctx, pool := plusDatabase(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	org, other := "10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000002"
	loc, loc2 := "20000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"
	tpl, v1, v2, v3 := "30000000-0000-0000-0000-000000000001", "40000000-0000-0000-0000-000000000001", "40000000-0000-0000-0000-000000000002", "40000000-0000-0000-0000-000000000003"
	item1, item2 := "50000000-0000-0000-0000-000000000001", "50000000-0000-0000-0000-000000000002"
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Uno','RESTAURANT'),($2,'Dos','RESTAURANT')`, org, other)
	exec(`INSERT INTO application_users(id,email,display_name) VALUES('admin','a@example.invalid','Admin'),('chef','c@example.invalid','Chef'),('worker','w@example.invalid','Worker'),('outsider','o@example.invalid','Outsider')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'chef','CHEF'),($1,'worker','WORKER'),($2,'outsider','ADMIN')`, org, other)
	// Lima (UTC-5) y Tokio (UTC+9) distinguen la zona horaria de cada sede.
	exec(`INSERT INTO locations(id,organization_id,name,address,timezone) VALUES($1,$3,'Lima','Dirección','America/Lima'),($2,$3,'Tokio','Dirección','Asia/Tokyo')`, loc, loc2, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2),($1,'chef',$3)`, org, loc, loc2)
	svc := application.New(New(pool))
	assignments := application.NewAssignments(New(pool))
	item := func(id string, order int) domain.Item {
		return domain.Item{ID: id, SectionName: "Higiene", Label: "Pregunta " + id[len(id)-1:], ResponseType: "COMPLIANCE", DisplayOrder: order, Required: true}
	}
	if _, e := svc.CreateTemplate(ctx, "admin", org, domain.Template{ID: tpl, VersionID: v1, Name: "Control", Area: "Cocina", Items: []domain.Item{item(item1, 1)}}); e != nil {
		t.Fatal(e)
	}

	// Turnos relativos a la hora local de cada sede: uno abierto y otro por abrir.
	local := func(zone string, minutes int) string {
		z, _ := time.LoadLocation(zone)
		at := time.Now().In(z).Add(time.Duration(minutes) * time.Minute)
		return shiftwindow.Format(at.Hour()*60 + at.Minute())
	}
	addShift := func(id, site, zone, name string, from, to int) {
		exec(`INSERT INTO location_shifts(id,organization_id,location_id,name,start_time,end_time) VALUES($1,$2,$3,$4,$5::time,$6::time)`, id, org, site, name, local(zone, from), local(zone, to))
	}
	openShift, laterShift, tokyoShift := "60000000-0000-0000-0000-000000000001", "60000000-0000-0000-0000-000000000002", "60000000-0000-0000-0000-000000000003"
	addShift(openShift, loc, "America/Lima", "Abierto", -60, 120)
	addShift(laterShift, loc, "America/Lima", "Luego", 180, 240)
	addShift(tokyoShift, loc2, "Asia/Tokyo", "Tokio", -30, 60)

	t.Run("programación por turno: validación, unicidad y estado", func(t *testing.T) {
		base := domain.Assignment{ID: "81000000-0000-0000-0000-000000000001", TemplateID: tpl, LocationID: loc, Frequency: "DAILY", ShiftID: openShift}
		if _, e := assignments.Create(ctx, "chef", org, base); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("un chef programó", e)
		}
		if _, e := assignments.Create(ctx, "outsider", org, base); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("tenant ajeno programó", e)
		}
		var invalid *domain.ValidationError
		weekly := base
		weekly.Frequency = "WEEKLY"
		if _, e := assignments.Create(ctx, "admin", org, weekly); !errors.As(e, &invalid) || invalid.Fields["frequency"] == "" {
			t.Fatal("turno semanal", e)
		}
		due := base
		due.DueTime = "09:00"
		if _, e := assignments.Create(ctx, "admin", org, due); !errors.As(e, &invalid) || invalid.Fields["dueTime"] == "" {
			t.Fatal("hora con turno", e)
		}
		wrongSite := base
		wrongSite.ShiftID = tokyoShift
		if _, e := assignments.Create(ctx, "admin", org, wrongSite); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("turno de otra sede", e)
		}
		a, e := assignments.Create(ctx, "admin", org, base)
		if e != nil || a.ShiftID != openShift || a.ShiftName != "Abierto" || a.TemplateVersionNumber != 1 {
			t.Fatal(a, e)
		}
		if _, e = assignments.Create(ctx, "admin", org, base); e != nil {
			t.Fatal("alta idéntica", e)
		}
		dup := base
		dup.ID = "81000000-0000-0000-0000-000000000009"
		if _, e = assignments.Create(ctx, "admin", org, dup); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("mismo turno duplicado", e)
		}
		whole := dup
		whole.ShiftID = ""
		if _, e = assignments.Create(ctx, "admin", org, whole); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("control de día completo junto a turnos", e)
		}
		later := dup
		later.ID, later.ShiftID = "81000000-0000-0000-0000-000000000002", laterShift
		if _, e = assignments.Create(ctx, "admin", org, later); e != nil {
			t.Fatal("otro turno de la misma plantilla", e)
		}
		got, e := assignments.Get(ctx, "chef", org, base.ID)
		if e != nil || got.Period.ShiftState != "OPEN" || got.Period.EndsAt == nil || !got.Period.EndsAt.After(time.Now()) || got.Period.StartsAt.After(time.Now()) {
			t.Fatal("estado del turno abierto", got, e)
		}
		upcoming, e := assignments.Get(ctx, "chef", org, later.ID)
		if e != nil || upcoming.Period.ShiftState != "UPCOMING" || !upcoming.Period.StartsAt.After(time.Now()) {
			t.Fatal("estado del turno próximo", upcoming, e)
		}
	})

	t.Run("revisión única por turno y fuera de horario", func(t *testing.T) {
		created := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
		start := func(n int, assignment string) (domain.Run, error) {
			return svc.CreateRun(ctx, "chef", org, domain.Run{ID: fmt.Sprintf("82000000-0000-0000-0000-%012d", n), LocationID: loc, TemplateVersionID: v1,
				AssignmentID: assignment, ClientOperationID: fmt.Sprintf("83000000-0000-0000-0000-%012d", n), ClientCreatedAt: created})
		}
		if _, e := start(1, "81000000-0000-0000-0000-000000000002"); !errors.Is(e, domain.ErrShiftOutOfHours) {
			t.Fatal("inició un turno cerrado", e)
		}
		run, e := start(2, "81000000-0000-0000-0000-000000000001")
		if e != nil || run.ShiftID != openShift || run.ShiftName != "Abierto" || run.TemplateVersionNumber != 1 {
			t.Fatal(run, e)
		}
		if _, e = start(2, "81000000-0000-0000-0000-000000000001"); e != nil {
			t.Fatal("reintento idempotente", e)
		}
		if _, e = start(3, "81000000-0000-0000-0000-000000000001"); !errors.Is(e, domain.ErrAssignmentStarted) {
			t.Fatal("segunda revisión del mismo turno", e)
		}
		if _, e = start(4, ""); !errors.Is(e, domain.ErrAssignmentRequired) {
			t.Fatal("omitió la programación", e)
		}
		detail, e := assignments.Get(ctx, "chef", org, "81000000-0000-0000-0000-000000000001")
		if e != nil || detail.Run == nil || detail.Run.ID != run.ID {
			t.Fatal("pendiente del turno", detail, e)
		}
		if _, e = assignments.Deactivate(ctx, "admin", org, "81000000-0000-0000-0000-000000000009"); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal(e)
		}
	})

	t.Run("zona horaria de la sede decide el período", func(t *testing.T) {
		tokyo := domain.Assignment{ID: "81000000-0000-0000-0000-000000000003", TemplateID: tpl, LocationID: loc2, Frequency: "DAILY", ShiftID: tokyoShift}
		if _, e := assignments.Create(ctx, "admin", org, tokyo); e != nil {
			t.Fatal(e)
		}
		got, e := assignments.Get(ctx, "chef", org, tokyo.ID)
		if e != nil || got.Timezone != "Asia/Tokyo" || got.Period.ShiftState != "OPEN" {
			t.Fatal("turno de Tokio abierto con su hora local", got, e)
		}
		// El mismo turno evaluado seis horas antes (hora de otra zona) no estaría abierto.
		w, _ := shiftwindow.New(got.ShiftStart, got.ShiftEnd)
		z, _ := time.LoadLocation("Asia/Tokyo")
		if !w.ContainsInstant(time.Now(), z) || w.ContainsInstant(time.Now().Add(-6*time.Hour), z) {
			t.Fatal("ventana inconsistente con la zona", got.ShiftStart, got.ShiftEnd)
		}
	})

	t.Run("versiones inmutables y ejecuciones antiguas", func(t *testing.T) {
		newVersion := domain.NewVersion{VersionID: v2, BaseVersionNumber: 1, ChangeNote: "Agrega pregunta", Items: []domain.Item{item(item1, 1), item(item2, 2)}}
		oldItem := "50000000-0000-0000-0000-0000000000a1"
		newVersion.Items[0].ID = oldItem // los ítems de cada versión son filas propias
		if _, e := svc.PublishVersion(ctx, "chef", org, tpl, newVersion); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("un chef publicó", e)
		}
		if _, e := svc.PublishVersion(ctx, "outsider", org, tpl, newVersion); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("tenant ajeno publicó", e)
		}
		if _, e := svc.PublishVersion(ctx, "admin", other, tpl, newVersion); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("publicó en otra empresa", e)
		}
		stale := newVersion
		stale.BaseVersionNumber = 5
		if _, e := svc.PublishVersion(ctx, "admin", org, tpl, stale); !errors.Is(e, domain.ErrVersionConflict) {
			t.Fatal("base obsoleta", e)
		}
		published, e := svc.PublishVersion(ctx, "admin", org, tpl, newVersion)
		if e != nil || published.VersionNumber != 2 || published.VersionID != v2 || len(published.Items) != 2 {
			t.Fatal(published, e)
		}
		again, e := svc.PublishVersion(ctx, "admin", org, tpl, newVersion)
		if e != nil || again.VersionID != v2 {
			t.Fatal("reintento idempotente", e)
		}
		changed := newVersion
		changed.ChangeNote = "Otra nota"
		if _, e = svc.PublishVersion(ctx, "admin", org, tpl, changed); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("mismo UUID con otro contenido", e)
		}
		var status string
		pool.QueryRow(ctx, `SELECT status FROM checklist_template_versions WHERE id=$1`, v1).Scan(&status)
		if status != "ARCHIVED" {
			t.Fatal("la versión anterior debe archivarse", status)
		}
		var items int
		pool.QueryRow(ctx, `SELECT count(*) FROM checklist_items WHERE template_version_id=$1`, v1).Scan(&items)
		if items != 1 {
			t.Fatal("la versión 1 cambió", items)
		}
		// La ejecución iniciada con la versión 1 conserva preguntas y número, y puede terminarse.
		run, e := svc.Run(ctx, "chef", org, "82000000-0000-0000-0000-000000000002")
		if e != nil || run.TemplateVersionNumber != 1 || run.TemplateVersionID != v1 || len(run.Items) != 1 {
			t.Fatal("ejecución antigua", run, e)
		}
		if run, e = svc.SaveAnswers(ctx, "chef", org, run.ID, run.RowVersion, "old-run-save", []domain.Answer{{ItemID: item1, ComplianceResult: "COMPLIANT"}}); e != nil {
			t.Fatal("guardar en versión archivada", e)
		}
		if run, e = svc.Submit(ctx, "chef", org, run.ID, run.RowVersion, "old-run-submit"); e != nil || run.Status != "SUBMITTED" || run.TemplateVersionNumber != 1 {
			t.Fatal("enviar en versión archivada", run, e)
		}
		// Iniciar con la versión vieja se rechaza; con la vigente funciona.
		oldStart := domain.Run{ID: "82000000-0000-0000-0000-000000000011", LocationID: loc2, TemplateVersionID: v1, ClientOperationID: "83000000-0000-0000-0000-000000000011", ClientCreatedAt: time.Now().Add(-time.Minute)}
		if _, e = svc.CreateRun(ctx, "chef", org, oldStart); !errors.Is(e, domain.ErrTemplateOutdated) && !errors.Is(e, domain.ErrAssignmentRequired) {
			t.Fatal("inició con versión archivada", e)
		}
		a, e := assignments.Get(ctx, "admin", org, "81000000-0000-0000-0000-000000000001")
		if e != nil || a.TemplateVersionID != v2 || a.TemplateVersionNumber != 2 {
			t.Fatal("la asignación debe apuntar a la versión vigente", a, e)
		}
		catalog, e := svc.Template(ctx, "chef", org, tpl)
		if e != nil || catalog.VersionID != v2 {
			t.Fatal("catálogo", catalog, e)
		}
		list, e := svc.TemplateVersions(ctx, "chef", org, tpl, pagination.Request{Size: 1})
		if e != nil || len(list.Items) != 1 || list.Items[0].Number != 2 || list.Items[0].Status != "PUBLISHED" || list.NextToken == "" {
			t.Fatal("historial", list, e)
		}
		rest, e := svc.TemplateVersions(ctx, "chef", org, tpl, pagination.Request{Size: 1, Token: list.NextToken})
		if e != nil || len(rest.Items) != 1 || rest.Items[0].Number != 1 || rest.Items[0].Status != "ARCHIVED" || rest.Items[0].ItemCount != 1 {
			t.Fatal("página siguiente", rest, e)
		}
		old, e := svc.TemplateVersion(ctx, "chef", org, tpl, v1)
		if e != nil || old.VersionNumber != 1 || len(old.Items) != 1 {
			t.Fatal("versión archivada", old, e)
		}
		if _, e = svc.TemplateVersions(ctx, "outsider", org, tpl, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("tenant ajeno leyó versiones", e)
		}
		if _, e = svc.TemplateVersion(ctx, "outsider", other, tpl, v1); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("versión de otro tenant", e)
		}
		third := domain.NewVersion{VersionID: v3, BaseVersionNumber: 2, Items: []domain.Item{item("50000000-0000-0000-0000-0000000000b1", 1)}}
		if _, e = svc.PublishVersion(ctx, "admin", org, tpl, third); e != nil {
			t.Fatal(e)
		}
		var published3 int
		pool.QueryRow(ctx, `SELECT count(*) FROM checklist_template_versions WHERE template_id=$1 AND status='PUBLISHED'`, tpl).Scan(&published3)
		if published3 != 1 {
			t.Fatal("debe existir una sola versión vigente", published3)
		}
	})
}
