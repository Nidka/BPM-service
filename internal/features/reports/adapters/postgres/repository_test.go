package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/application"
	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	orgA = "org-a"
	orgB = "org-b"
)

func uid(kind, n int) string { return fmt.Sprintf("%08d-0000-4000-8000-%012d", kind, n) }

var (
	locLima  = uid(1, 1)
	locTokyo = uid(1, 2)
	locForgn = uid(1, 3)
)

// database crea un schema aislado en bpm_test y aplica la cadena V1,V2,V4..V8 y V10.
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
	schema := pgx.Identifier{fmt.Sprintf("reports_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql", "V7__alerts_notifications.sql", "V8__training_academy.sql", "V10__reports_usage.sql"} {
		if name == "V10__reports_usage.sql" && New(pool).RequireSchema(ctx) == nil {
			t.Fatal("reportes arrancan sin V10")
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
	return ctx, pool
}

func exec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err, sql)
	}
}

// seed crea dos empresas, tres sedes (Lima, Tokio, ajena), usuarios por rol y una plantilla publicada.
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, ctx, pool, `INSERT INTO organizations(id,legal_name,trade_name,business_type) VALUES($1,'Empresa A SAC','Empresa A','RESTAURANT'),($2,'Empresa B','Empresa B','RESTAURANT')`, orgA, orgB)
	exec(t, ctx, pool, `INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','admin@example.invalid','Admin','USER'),('chef','chef@example.invalid','Chef','USER'),('worker','worker@example.invalid','Worker','USER'),('other','other@example.invalid','Otro','USER'),('super','super@example.invalid','Super','SUPER_ADMIN')`)
	exec(t, ctx, pool, `INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'chef','CHEF'),($1,'worker','WORKER'),($2,'other','ADMIN')`, orgA, orgB)
	exec(t, ctx, pool, `INSERT INTO locations(id,organization_id,name,address,timezone) VALUES($1,$4,'=Lima','x','America/Lima'),($2,$4,'Tokio','x','Asia/Tokyo'),($3,$5,'Ajena','x','America/Lima')`, locLima, locTokyo, locForgn, orgA, orgB)
	exec(t, ctx, pool, `INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2)`, orgA, locLima)
	exec(t, ctx, pool, `INSERT INTO checklist_templates(id,organization_id,name,area) VALUES($1,$3,'Higiene','COCINA'),($2,$4,'Ajena','COCINA')`, uid(2, 1), uid(2, 2), orgA, orgB)
	exec(t, ctx, pool, `INSERT INTO checklist_template_versions(id,organization_id,template_id,version_number,status) VALUES($1,$3,$5,1,'PUBLISHED'),($2,$4,$6,1,'PUBLISHED')`, uid(3, 1), uid(3, 2), orgA, orgB, uid(2, 1), uid(2, 2))
}

var n int

func run(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org, loc, version, user string, score float64, at time.Time, assignment any) {
	t.Helper()
	n++
	exec(t, ctx, pool, `INSERT INTO checklist_runs(id,organization_id,location_id,template_version_id,assignment_id,performed_by,status,compliance_percent,client_operation_id,client_created_at,submitted_at) VALUES($1,$2,$3,$4,$5,$6,'SUBMITTED',$7,$8,$9,$9)`,
		uid(9, n), org, loc, version, assignment, user, score, uid(8, n), at)
}

type collector struct {
	begun bool
	rows  []domain.ExportRow
}

func (c *collector) Begin() error                 { c.begun = true; return nil }
func (c *collector) Row(r domain.ExportRow) error { c.rows = append(c.rows, r); return nil }

func localDay(at time.Time, zone string) string {
	loc, _ := time.LoadLocation(zone)
	return at.In(loc).Format(domain.DateLayout)
}

func TestCumplimientoZonaHorariaTenantYRoles(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	repo := New(pool)
	svc := application.New(repo, repo)
	base := time.Now().UTC().AddDate(0, 0, -20).Truncate(24 * time.Hour)
	at1, at2 := base.Add(3*time.Hour), base.Add(20*time.Hour)
	run(t, ctx, pool, orgA, locLima, uid(3, 1), "admin", 90, at1, nil)
	run(t, ctx, pool, orgA, locLima, uid(3, 1), "chef", 70, at2, nil)
	run(t, ctx, pool, orgA, locTokyo, uid(3, 1), "admin", 100, at1, nil)
	run(t, ctx, pool, orgB, locForgn, uid(3, 2), "other", 10, at1, nil)
	exec(t, ctx, pool, `INSERT INTO checklist_runs(id,organization_id,location_id,template_version_id,performed_by,status,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'admin','DRAFT',$5,NOW())`, uid(9, 900), orgA, locLima, uid(3, 1), uid(8, 900))
	in := application.PeriodInput{From: base.AddDate(0, 0, -3).Format(domain.DateLayout), To: base.AddDate(0, 0, 3).Format(domain.DateLayout)}

	rep, err := svc.Compliance(ctx, "admin", orgA, in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Runs != 3 || *rep.Average != 86.67 || len(rep.Locations) != 2 {
		t.Fatalf("empresa: %+v", rep)
	}
	var lima domain.LocationCompliance
	for _, l := range rep.Locations {
		if l.LocationID == locLima {
			lima = l
		}
	}
	d1, d2 := localDay(at1, "America/Lima"), localDay(at2, "America/Lima")
	if d1 == d2 || len(lima.Days) != 2 || lima.Days[0].Date != d1 || *lima.Days[0].Average != 90 || lima.Days[1].Date != d2 {
		t.Fatalf("la zona de la sede decide el día: %+v (%s %s)", lima.Days, d1, d2)
	}
	for _, l := range rep.Locations {
		if l.LocationID == locTokyo && l.Days[0].Date != localDay(at1, "Asia/Tokyo") {
			t.Fatalf("día de Tokio: %+v", l.Days)
		}
	}

	// CHEF solo ve su sede; una sede fuera de su alcance o ajena responde "no encontrada".
	chef, err := svc.Compliance(ctx, "chef", orgA, in)
	if err != nil || chef.Runs != 2 || *chef.Average != 80 || len(chef.Locations) != 1 {
		t.Fatalf("chef: %+v %v", chef, err)
	}
	for _, c := range []struct{ user, loc string }{{"chef", locTokyo}, {"admin", locForgn}, {"admin", "no-es-uuid"}} {
		if _, err = svc.Compliance(ctx, c.user, orgA, application.PeriodInput{From: in.From, To: in.To, LocationID: c.loc}); !errors.Is(err, domain.ErrLocationNotFound) {
			t.Fatalf("%v: %v", c, err)
		}
	}
	// WORKER y miembros de otra empresa no acceden; el tenant sale de la membresía.
	for _, user := range []string{"worker", "other", "nadie"} {
		if _, err = svc.Compliance(ctx, user, orgA, in); !errors.Is(err, domain.ErrDenied) {
			t.Fatalf("%s: %v", user, err)
		}
	}
	if _, err = svc.KPIs(ctx, "worker", orgA); !errors.Is(err, domain.ErrDenied) {
		t.Fatal(err)
	}
	if _, err = svc.Compliance(ctx, "super", orgA, in); err != nil {
		t.Fatalf("SUPER_ADMIN debe poder consultar: %v", err)
	}
	// Límites de período.
	long := application.PeriodInput{From: base.AddDate(0, 0, -100).Format(domain.DateLayout), To: base.Format(domain.DateLayout)}
	var v *domain.ValidationError
	if _, err = svc.Compliance(ctx, "admin", orgA, long); !errors.As(err, &v) {
		t.Fatalf("período largo: %v", err)
	}

	// Exportación CSV: filas del alcance, texto escapado y sin personas.
	sink := &collector{}
	if err = svc.ExportRuns(ctx, "chef", orgA, in, sink); err != nil || !sink.begun || len(sink.rows) != 2 {
		t.Fatalf("export: %v %+v", err, sink)
	}
	if rec := sink.rows[0].Record(); !strings.HasPrefix(rec[2], "'=") || len(rec) != len(domain.ExportHeader) {
		t.Fatalf("sin escape: %v", rec)
	}
	if sink.rows[0].Zone == "" {
		t.Fatal("la fila exportada debe traer la zona horaria de la sede")
	}
	// Exportación PDF: mismas filas y alcance, auditada con su propia acción.
	doc, err := svc.ExportRunsDocument(ctx, "chef", orgA, in)
	if err != nil || len(doc.Rows) != 2 || doc.OrganizationName == "" || doc.GeneratedAt.IsZero() {
		t.Fatalf("pdf: %v %+v", err, doc)
	}
	var audited int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='CHECKLIST_HISTORY_EXPORTED_PDF' AND actor_user_id='chef'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("auditoría del PDF: %d %v", audited, err)
	}
	if _, err = svc.ExportRunsDocument(ctx, "worker", orgA, in); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("worker no exporta PDF: %v", err)
	}
	if _, err = svc.WithExportLimit(2).ExportRunsDocument(ctx, "admin", orgA, in); !errors.Is(err, domain.ErrPDFTooLarge) {
		t.Fatalf("límite de filas del PDF: %v", err)
	}
	denied := &collector{}
	if err = svc.ExportRuns(ctx, "worker", orgA, in, denied); !errors.Is(err, domain.ErrDenied) || denied.begun {
		t.Fatal("worker no exporta")
	}
	big := &collector{}
	if err = svc.WithExportLimit(2).ExportRuns(ctx, "admin", orgA, in, big); !errors.Is(err, domain.ErrExportTooLarge) || big.begun {
		t.Fatalf("límite de filas: %v", err)
	}
}

func TestResumenKPIsYCapacitacion(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	repo := New(pool)
	svc := application.New(repo, repo)
	now := time.Now().UTC()
	// Controles: una asignación diaria pendiente y otra cumplida hoy.
	exec(t, ctx, pool, `INSERT INTO checklist_assignments(id,organization_id,location_id,template_id,frequency,due_time) VALUES($1,$3,$4,$5,'DAILY','00:00'),($2,$3,$4,$5,'DAILY',NULL)`, uid(4, 1), uid(4, 2), orgA, locLima, uid(2, 1))
	run(t, ctx, pool, orgA, locLima, uid(3, 1), "admin", 80, now.Add(-time.Minute), uid(4, 2))
	// Incidencias y temperatura.
	exec(t, ctx, pool, `INSERT INTO incident_catalog(id,organization_id,code,name,area,normative_criterion,risk_level) VALUES($1,$2,'C1','Cat','COCINA','x','HIGH')`, uid(5, 1), orgA)
	exec(t, ctx, pool, `INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,status,client_operation_id,client_created_at,closed_at) VALUES($1,$2,$3,$4,'admin','OPEN',$5,NOW(),NULL),($6,$2,$3,$4,'admin','CLOSED',$7,NOW(),NOW())`, uid(6, 1), orgA, locLima, uid(5, 1), uid(8, 801), uid(6, 2), uid(8, 802))
	exec(t, ctx, pool, `INSERT INTO suppliers(id,organization_id,name) VALUES($1,$2,'Prov')`, uid(7, 1), orgA)
	exec(t, ctx, pool, `INSERT INTO products(id,organization_id,name,unit,requires_temperature,minimum_temperature,maximum_temperature) VALUES($1,$2,'Pollo','KG',TRUE,0,5)`, uid(7, 2), orgA)
	exec(t, ctx, pool, `INSERT INTO supply_receipts(id,organization_id,location_id,supplier_id,received_by,received_at,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'admin',NOW(),$5,NOW())`, uid(7, 3), orgA, locLima, uid(7, 1), uid(8, 803))
	exec(t, ctx, pool, `INSERT INTO supply_receipt_items(organization_id,receipt_id,product_id,quantity,temperature_celsius,decision,minimum_temperature,maximum_temperature) VALUES($1,$2,$3,1,9,'ACCEPTED',0,5),($1,$2,$3,1,3,'ACCEPTED',0,5)`, orgA, uid(7, 3), uid(7, 2))
	// Alertas: una abierta y una atendida.
	exec(t, ctx, pool, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,status) VALUES($1,$2,'admin','CHECKLIST_MISSING','t','m','PENDING'),($1,$2,'admin','CHECKLIST_MISSING','t','m','ATTENDED')`, orgA, locLima)
	// Capacitación: dos cursos publicados, progreso de admin y chef; la empresa B no cuenta.
	exec(t, ctx, pool, `INSERT INTO training_courses(id,organization_id,code,title,status) VALUES($1,$3,'C1','Curso 1','PUBLISHED'),($2,$3,'C2','Curso 2','DRAFT'),($4,$5,'C1','Ajeno','PUBLISHED')`, uid(10, 1), uid(10, 2), orgA, uid(10, 3), orgB)
	exec(t, ctx, pool, `INSERT INTO training_progress(organization_id,user_id,course_id,status,best_score) VALUES($1,'admin',$2,'COMPLETED',90),($1,'chef',$2,'IN_PROGRESS',60),($3,'other',$4,'COMPLETED',100)`, orgA, uid(10, 1), orgB, uid(10, 3))

	k, err := svc.KPIs(ctx, "admin", orgA)
	if err != nil {
		t.Fatal(err)
	}
	if k.Pending.Assigned != 2 || k.Pending.Pending != 1 || k.Pending.Overdue != 1 || k.OpenAlerts != 1 || k.OpenIncidents != 1 || k.Last30Days.Runs != 1 || *k.Last30Days.Average != 80 {
		t.Fatalf("kpis: %+v", k)
	}
	if k.Training == nil || k.Training.PublishedCourses != 1 || k.Training.Learners != 3 || k.Training.CompletedEnrollments != 1 || *k.Training.AverageBestScore != 75 {
		t.Fatalf("capacitación: %+v", k.Training)
	}
	if c, err := svc.KPIs(ctx, "chef", orgA); err != nil || c.Training != nil || c.Pending.Assigned != 2 {
		t.Fatalf("chef: %+v %v", c, err)
	}

	day := now.In(domain.Lima).Format(domain.DateLayout)
	s, err := svc.Summary(ctx, "admin", orgA, application.PeriodInput{From: day, To: day})
	if err != nil {
		t.Fatal(err)
	}
	if s.Incidents.Reported != 1 && s.Incidents.Reported != 2 || s.Incidents.OpenNow != 1 || s.Temperatures.Measurements != 2 || s.Temperatures.OutOfRange != 1 || len(s.Areas) != 1 || s.Areas[0].Area != "COCINA" || len(s.Locations) != 2 {
		t.Fatalf("resumen: %+v", s)
	}

	courses, err := svc.TrainingCourses(ctx, "admin", orgA)
	if err != nil || len(courses) != 1 || courses[0].Assignable != 3 || courses[0].Enrolled != 2 || courses[0].InProgress != 1 || courses[0].Completed != 1 || *courses[0].AverageBestScore != 75 {
		t.Fatalf("cursos: %+v %v", courses, err)
	}
	if _, err = svc.TrainingCourses(ctx, "chef", orgA); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("chef no ve avance por persona")
	}
	page, err := svc.TrainingPeople(ctx, "admin", orgA, "", pagination.Request{Size: 2})
	if err != nil || len(page.Items) != 2 || page.NextToken == "" {
		t.Fatalf("página 1: %+v %v", page, err)
	}
	next, err := svc.TrainingPeople(ctx, "admin", orgA, "", pagination.Request{Size: 2, Token: page.NextToken})
	if err != nil || len(next.Items) != 1 || next.NextToken != "" || next.Items[0].UserID == page.Items[0].UserID {
		t.Fatalf("página 2: %+v %v", next, err)
	}
	if _, err = svc.TrainingPeople(ctx, "admin", orgA, uid(10, 3), pagination.Request{}); !errors.Is(err, domain.ErrCourseNotFound) {
		t.Fatalf("curso de otra empresa: %v", err)
	}
}

func TestConstanciasUmbralIdempotenciaInmutabilidad(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	repo := New(pool)
	svc := application.New(repo, repo)
	base := time.Now().UTC().AddDate(0, 0, -20).Truncate(24 * time.Hour)
	for i, score := range []float64{90, 80, 85} {
		run(t, ctx, pool, orgA, locLima, uid(3, 1), "admin", score, base.Add(time.Duration(12+i)*time.Hour), nil)
	}
	from, to := base.AddDate(0, 0, -1).Format(domain.DateLayout), base.AddDate(0, 0, 1).Format(domain.DateLayout)
	if _, err := svc.IssueCertificate(ctx, "chef", orgA, from, to); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("solo ADMIN emite")
	}
	first, err := svc.IssueCertificate(ctx, "admin", orgA, from, to)
	if err != nil || !first.Created || first.Certificate.AverageCompliance != 85 || first.Certificate.Threshold != 80 || !domain.ValidVerificationCode(first.Certificate.VerificationCode) {
		t.Fatalf("emisión: %+v %v", first, err)
	}
	// Subir el umbral no cambia ni vuelve a emitir la constancia del mismo período.
	if _, err = svc.UpdateSettings(ctx, "admin", orgA, domain.Settings{WeeklyEmailEnabled: true, ComplianceThreshold: 95}); err != nil {
		t.Fatal(err)
	}
	again, err := svc.IssueCertificate(ctx, "admin", orgA, from, to)
	if err != nil || again.Created || again.Certificate.ID != first.Certificate.ID {
		t.Fatalf("idempotencia: %+v %v", again, err)
	}
	// Otro período con umbral más alto que el promedio: rechazo; sin ejecuciones: sin datos.
	var th *domain.ThresholdError
	if _, err = svc.IssueCertificate(ctx, "admin", orgA, base.Format(domain.DateLayout), to); !errors.As(err, &th) || th.Threshold != 95 {
		t.Fatalf("umbral: %v", err)
	}
	if _, err = svc.IssueCertificate(ctx, "admin", orgA, base.AddDate(0, 0, -10).Format(domain.DateLayout), base.AddDate(0, 0, -9).Format(domain.DateLayout)); !errors.Is(err, domain.ErrNoData) {
		t.Fatalf("sin datos: %v", err)
	}
	if _, err = svc.IssueCertificate(ctx, "admin", orgA, from, time.Now().UTC().Format(domain.DateLayout)); err == nil {
		t.Fatal("un período no terminado no se certifica")
	}
	// Inmutable.
	if _, err = pool.Exec(ctx, `UPDATE compliance_certificates SET average_compliance=100`); err == nil {
		t.Fatal("la constancia debe ser inmutable")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM compliance_certificates`); err == nil {
		t.Fatal("la constancia no se borra")
	}
	// Verificación pública por código; códigos desconocidos o mal formados dan 404.
	pub, err := svc.VerifyCertificate(ctx, first.Certificate.VerificationCode)
	if err != nil || pub.OrganizationName != "Empresa A" || pub.AverageCompliance != 85 {
		t.Fatalf("verificación: %+v %v", pub, err)
	}
	for _, code := range []string{"AAAAAAAAAAAAAAAAAAAAAAAA", "x", "' OR 1=1 --"} {
		if _, err = svc.VerifyCertificate(ctx, code); !errors.Is(err, domain.ErrCertificateNotFound) {
			t.Fatalf("%q: %v", code, err)
		}
	}
	// La empresa B no ve las constancias de A.
	list, err := svc.ListCertificates(ctx, "other", orgB, pagination.Request{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("aislamiento: %+v %v", list, err)
	}
	list, err = svc.ListCertificates(ctx, "admin", orgA, pagination.Request{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("lista: %+v %v", list, err)
	}
	if _, err = svc.ListCertificates(ctx, "admin", orgB, pagination.Request{}); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("tenant manipulado")
	}
}

type mailer struct {
	sent []string
	fail error
}

func (m *mailer) Send(_ context.Context, key, to, subject, body string) (string, error) {
	if m.fail != nil {
		return "", m.fail
	}
	if strings.Contains(body, "@example.invalid") {
		panic("el correo no debe incluir direcciones de terceros")
	}
	m.sent = append(m.sent, to+"|"+key)
	return "msg-" + key, nil
}

func count(t *testing.T, ctx context.Context, pool *pgxpool.Pool, where string) int {
	t.Helper()
	var c int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM weekly_report_deliveries WHERE `+where).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorreoSemanalIdempotenteYConfigurable(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	repo := New(pool)
	svc := application.New(repo, repo)
	ok := &mailer{}
	job := application.NewWeeklyJob(repo, ok)
	now := time.Now().UTC()
	// La empresa B desactiva el correo.
	if _, err := svc.UpdateSettings(ctx, "other", orgB, domain.Settings{WeeklyEmailEnabled: false, ComplianceThreshold: 80}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := job.Stage(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, ctx, pool, `organization_id='`+orgA+`'`) != 1 || count(t, ctx, pool, `organization_id='`+orgB+`'`) != 0 {
		t.Fatal("una entrega por ADMIN y semana; B desactivada")
	}
	if err := job.SendDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err := job.SendDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(ok.sent) != 1 || !strings.HasPrefix(ok.sent[0], "admin@example.invalid|bpm-weekly/") || count(t, ctx, pool, `status='SENT'`) != 1 {
		t.Fatalf("envío único: %v", ok.sent)
	}
	// Un fallo temporal reprograma; un ADMIN degradado cancela la entrega.
	exec(t, ctx, pool, `DELETE FROM weekly_report_deliveries`)
	flaky := &mailer{fail: errors.New("caído")}
	job = application.NewWeeklyJob(repo, flaky)
	if err := job.Stage(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := job.SendDue(ctx, 5); err != nil {
		t.Fatal(err)
	}
	var next time.Time
	if count(t, ctx, pool, `status='PENDING' AND attempts=1`) != 1 {
		t.Fatal("debía quedar pendiente con un intento")
	}
	pool.QueryRow(ctx, `SELECT next_attempt_at FROM weekly_report_deliveries`).Scan(&next)
	if !next.After(now) {
		t.Fatal("reintento sin espera")
	}
	exec(t, ctx, pool, `UPDATE weekly_report_deliveries SET next_attempt_at=NOW()-interval '1 minute'`)
	exec(t, ctx, pool, `UPDATE organization_memberships SET role='WORKER' WHERE user_id='admin'`)
	job = application.NewWeeklyJob(repo, ok)
	if err := job.SendDue(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if count(t, ctx, pool, `status='CANCELLED'`) != 1 || len(ok.sent) != 1 {
		t.Fatal("un destinatario que ya no es ADMIN no recibe el correo")
	}
	// Rechazo permanente.
	exec(t, ctx, pool, `DELETE FROM weekly_report_deliveries`)
	exec(t, ctx, pool, `UPDATE organization_memberships SET role='ADMIN' WHERE user_id='admin'`)
	job = application.NewWeeklyJob(repo, &mailer{fail: application.ErrMailPermanent})
	if err := job.Stage(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := job.SendDue(ctx, 5); err != nil || count(t, ctx, pool, `status='FAILED'`) != 1 {
		t.Fatalf("rechazo permanente: %v", err)
	}
}
