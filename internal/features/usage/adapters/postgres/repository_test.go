package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/usage/application"
	"github.com/Nidka/BPM-service/internal/features/usage/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func uid(kind, n int) string { return fmt.Sprintf("%08d-0000-4000-8000-%012d", kind, n) }

func database(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("usage_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"V1__baseline.sql", "V9__research_cap.sql", "V10__reports_usage.sql"} {
		if name == "V10__reports_usage.sql" && New(pool).RequireSchema(ctx) == nil {
			t.Fatal("el registro de uso arranca sin V10")
		}
		sql, err := os.ReadFile("../../../../../migraciones/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(name, err)
		}
	}
	if err = New(pool).RequireSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO organizations(id,legal_name,business_type) VALUES('org-a','A','RESTAURANT'),('org-b','B','RESTAURANT')`,
		`INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','a@example.invalid','A','USER'),('worker','w@example.invalid','W','USER'),('other','o@example.invalid','O','USER'),('super','s@example.invalid','S','SUPER_ADMIN')`,
		`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES('org-a','admin','ADMIN'),('org-a','worker','WORKER'),('org-b','other','ADMIN')`,
	} {
		if _, err = pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, pool
}

func ev(n int, session int, typ, module string, at time.Time) domain.Event {
	return domain.Event{ID: uid(2, n), SessionID: uid(1, session), Type: typ, Module: module, OccurredAt: at}
}

func TestRegistroDeUsoIdempotenteYAislado(t *testing.T) {
	ctx, pool := database(t)
	svc := application.New(New(pool))
	now := time.Now().UTC()
	secs := 90
	end := ev(3, 1, domain.SessionEnd, "", now.Add(-time.Minute))
	end.DurationSeconds = &secs
	batch := []domain.Event{ev(1, 1, domain.SessionStart, "", now.Add(-10*time.Minute)), ev(2, 1, domain.ModuleVisit, "CHECKLISTS", now.Add(-5*time.Minute)), end}

	r, err := svc.Record(ctx, "worker", "org-a", batch)
	if err != nil || r.Accepted != 3 || r.Duplicates != 0 {
		t.Fatalf("primer envío: %+v %v", r, err)
	}
	r, err = svc.Record(ctx, "worker", "org-a", batch) // reintento idéntico
	if err != nil || r.Accepted != 0 || r.Duplicates != 3 {
		t.Fatalf("reintento: %+v %v", r, err)
	}
	var events, sessions int
	pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM usage_events), (SELECT count(*) FROM usage_sessions)`).Scan(&events, &sessions)
	var meta string
	pool.QueryRow(ctx, `SELECT DISTINCT metadata::text FROM usage_events`).Scan(&meta)
	if events != 3 || sessions != 1 || meta != "{}" {
		t.Fatalf("filas=%d sesiones=%d metadata=%s", events, sessions, meta)
	}
	// El mismo UUID con otro contenido es un conflicto; un lote inválido no guarda nada.
	changed := batch[1]
	changed.Module = "ALERTS"
	if _, err = svc.Record(ctx, "worker", "org-a", []domain.Event{changed}); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("conflicto: %v", err)
	}
	var v *domain.ValidationError
	bad := []domain.Event{ev(10, 1, domain.ModuleVisit, "CHECKLISTS", now), ev(11, 1, "SCREENSHOT", "", now)}
	if _, err = svc.Record(ctx, "worker", "org-a", bad); !errors.As(err, &v) || v.Fields["events[1].type"] == "" {
		t.Fatalf("tipo inválido: %v", err)
	}
	if _, err = svc.Record(ctx, "worker", "org-a", []domain.Event{ev(12, 1, domain.ModuleVisit, "CHECKLISTS", now.Add(-8*24*time.Hour))}); !errors.As(err, &v) {
		t.Fatal("fecha antigua")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM usage_events`).Scan(&events)
	if events != 3 {
		t.Fatal("un lote rechazado no debe guardar eventos")
	}
	// Otro usuario no puede escribir en una sesión ajena; otra empresa tampoco reutiliza el UUID.
	if _, err = svc.Record(ctx, "admin", "org-a", []domain.Event{ev(20, 1, domain.ModuleVisit, "HOME", now)}); !errors.Is(err, domain.ErrSessionConflict) {
		t.Fatalf("sesión ajena: %v", err)
	}
	if _, err = svc.Record(ctx, "other", "org-b", []domain.Event{ev(21, 1, domain.ModuleVisit, "HOME", now)}); !errors.Is(err, domain.ErrSessionConflict) {
		t.Fatalf("sesión de otra empresa: %v", err)
	}
	// Autorización: usuario de otra empresa y SUPER_ADMIN sin membresía no registran.
	for _, c := range []struct{ user, org string }{{"other", "org-a"}, {"super", "org-a"}, {"ghost", "org-a"}} {
		if _, err = svc.Record(ctx, c.user, c.org, []domain.Event{ev(30, 5, domain.ModuleVisit, "HOME", now)}); !errors.Is(err, domain.ErrDenied) {
			t.Fatalf("%v: %v", c, err)
		}
	}
	// Resumen: ADMIN y SUPER_ADMIN; WORKER no; agregados sin personas.
	// La sesión empezó hace 10 minutos: justo después de medianoche (Lima) cae en el día anterior.
	day := now.In(domain.Lima).Format(domain.DateLayout)
	from := now.Add(-10 * time.Minute).In(domain.Lima).Format(domain.DateLayout)
	s, err := svc.Summary(ctx, "admin", "org-a", from, day)
	if err != nil || s.Sessions != 1 || s.EndedSessions != 1 || s.ActiveUsers != 1 || s.AverageSessionSeconds == nil || *s.AverageSessionSeconds != 540 || len(s.Modules) != 1 || s.Modules[0].Module != "CHECKLISTS" {
		t.Fatalf("resumen: %+v %v", s, err)
	}
	if _, err = svc.Summary(ctx, "super", "org-a", from, day); err != nil {
		t.Fatalf("SUPER_ADMIN: %v", err)
	}
	if _, err = svc.Summary(ctx, "worker", "org-a", from, day); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("worker no consulta")
	}
	// Resumen de investigación: solo usuarios con participación ACTIVE y consentimiento vigente.
	rs, err := svc.ResearchSummary(ctx, "admin", "org-a", from, day)
	if err != nil || rs.Sessions != 0 || rs.ActiveUsers != 0 || len(rs.Modules) != 0 || rs.AlertsViewed != 0 {
		t.Fatalf("sin consentimiento no hay datos de investigación: %+v %v", rs, err)
	}
	if _, err = svc.ResearchSummary(ctx, "worker", "org-a", from, day); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("worker no consulta investigación")
	}
	for _, q := range []string{
		`INSERT INTO research_consent_documents(version,title,body_text,responsible_investigator) VALUES('v-old','t','b','i')`,
		`INSERT INTO research_participants(id,organization_id,user_id,participant_code) VALUES('00000000-0000-0000-0000-0000000000a1','org-a','worker','P-TEST-1')`,
		`INSERT INTO consent_records(organization_id,participant_id,document_version,accepted,accepted_at,recorded_by) VALUES('org-a','00000000-0000-0000-0000-0000000000a1','v-old',true,NOW(),'worker')`,
	} {
		if _, err = pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	rs, err = svc.ResearchSummary(ctx, "admin", "org-a", from, day)
	if err != nil || rs.Sessions != 1 || rs.ActiveUsers != 1 || len(rs.Modules) != 1 {
		t.Fatalf("con consentimiento vigente: %+v %v", rs, err)
	}
	// Un documento nuevo sin reaceptar o una revocación excluyen al usuario.
	if _, err = pool.Exec(ctx, `UPDATE research_consent_documents SET status='ARCHIVED'; INSERT INTO research_consent_documents(version,title,body_text,responsible_investigator) VALUES('v-new','t','b','i')`); err != nil {
		t.Fatal(err)
	}
	if rs, err = svc.ResearchSummary(ctx, "admin", "org-a", from, day); err != nil || rs.Sessions != 0 || rs.ActiveUsers != 0 {
		t.Fatalf("versión desactualizada: %+v %v", rs, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO consent_records(organization_id,participant_id,document_version,accepted,accepted_at,recorded_by) VALUES('org-a','00000000-0000-0000-0000-0000000000a1','v-new',true,NOW()+interval '1 second','worker')`); err != nil {
		t.Fatal(err)
	}
	if rs, err = svc.ResearchSummary(ctx, "admin", "org-a", from, day); err != nil || rs.Sessions != 1 {
		t.Fatalf("reaceptado: %+v %v", rs, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO consent_records(organization_id,participant_id,document_version,accepted,accepted_at,recorded_by) VALUES('org-a','00000000-0000-0000-0000-0000000000a1','v-new',false,NOW()+interval '2 seconds','worker')`); err != nil {
		t.Fatal(err)
	}
	if rs, err = svc.ResearchSummary(ctx, "admin", "org-a", from, day); err != nil || rs.Sessions != 0 {
		t.Fatalf("revocado: %+v %v", rs, err)
	}
	if all, err := svc.Summary(ctx, "admin", "org-a", from, day); err != nil || all.Sessions != 1 {
		t.Fatalf("el resumen operativo no cambia: %+v %v", all, err)
	}
	other, err := svc.Summary(ctx, "other", "org-b", day, day)
	if err != nil || other.Sessions != 0 {
		t.Fatalf("aislamiento: %+v %v", other, err)
	}
	if _, err = svc.Summary(ctx, "admin", "org-a", "2026-01-01", "2026-12-31"); !errors.As(err, &v) {
		t.Fatal("período máximo")
	}
}
