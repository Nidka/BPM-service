package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const org = "10000000-0000-0000-0000-000000000001"
const foreign = "10000000-0000-0000-0000-000000000002"

func uid(kind, n int) string { return fmt.Sprintf("%08d-0000-4000-8000-%012d", kind, n) }

// database crea un schema aislado en bpm_test y aplica V1 y V8. Se omite si no hay base disponible.
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("training_%d", time.Now().UnixNano())}.Sanitize()
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
	for _, name := range []string{"V1__baseline.sql", "V7__alerts_notifications.sql", "V8__training_academy.sql", "V11__training_assignments_paths.sql"} {
		if name == "V11__training_assignments_paths.sql" && New(pool).RequireSchema(ctx) == nil {
			t.Fatal("la Academia arranca sin V11")
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
		t.Fatal("no reconoce V8", err)
	}
	return ctx, pool
}

func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Organización','RESTAURANT'),($2,'Ajena','RESTAURANT')`, []any{org, foreign}},
		{`INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','admin@example.invalid','Admin','USER'),('chef','chef@example.invalid','Chef','USER'),('worker','worker@example.invalid','Worker','USER'),('other','other@example.invalid','Otro','USER'),('super','super@example.invalid','Super','SUPER_ADMIN')`, nil},
		{`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'chef','CHEF'),($1,'worker','WORKER'),($2,'other','ADMIN')`, []any{org, foreign}},
	} {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

type videos struct{ inspection contracts.VideoInspection }

func (videos) PresignVideoUpload(context.Context, contracts.UploadRequest) (contracts.SignedRequest, error) {
	return contracts.SignedRequest{URL: "https://signed.invalid/put", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (videos) PresignVideoDownload(context.Context, string) (contracts.SignedRequest, error) {
	return contracts.SignedRequest{URL: "https://signed.invalid/get", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (v videos) InspectVideo(context.Context, string) (contracts.VideoInspection, error) {
	return v.inspection, nil
}

// publish crea y publica un curso de 3 preguntas (respuesta correcta 1, mínimo 67).
func publish(t *testing.T, ctx context.Context, s *application.Service, code string, withVideo string) (string, string, domain.Draft) {
	t.Helper()
	course, err := s.CreateCourse(ctx, "admin", org, domain.NewCourse{Code: code, Title: "Curso " + code})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.Draft(ctx, "admin", org, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.DraftInput{PassingScore: 67, RowVersion: draft.Version.RowVersion, Lessons: []domain.Lesson{{ID: uid(3, 1), Title: "Lección", Content: "x", DisplayOrder: 1, VideoMediaID: withVideo}}}
	for i := 1; i <= 3; i++ {
		in.Questions = append(in.Questions, domain.Question{ID: uid(2, i), LessonID: uid(3, 1), Text: fmt.Sprintf("Q%d", i), Options: []string{"a", "b", "c"}, CorrectOption: 1, Explanation: "e", DisplayOrder: i})
	}
	saved, err := s.SaveDraft(ctx, "admin", org, course.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "admin", org, course.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.Course(ctx, "worker", org, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	return course.ID, view.Version.ID, saved
}

func attempt(version, id, op, kind string, picks ...int) domain.AttemptInput {
	in := domain.AttemptInput{ID: id, ClientOperationID: op, VersionID: version, AttemptType: kind, StartedAt: time.Now().Add(-time.Minute), ClientCreatedAt: time.Now()}
	for i, pick := range picks {
		in.Answers = append(in.Answers, domain.Answer{QuestionID: uid(2, i+1), SelectedOption: pick})
	}
	return in
}

func TestTrainingPostgresLifecycleTenantAndIdempotency(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	courseID, versionID, _ := publish(t, ctx, s, "M-01", "")

	// Aislamiento: otra organización no encuentra el curso; su usuario no entra a esta.
	if _, err := s.Course(ctx, "other", foreign, courseID); !errors.Is(err, domain.ErrCourseNotFound) {
		t.Fatal("el curso de otra organización debe ser 404", err)
	}
	if _, err := s.Course(ctx, "other", org, courseID); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("un usuario ajeno no debe entrar", err)
	}
	if _, err := s.Draft(ctx, "worker", org, courseID); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("un trabajador no administra", err)
	}

	// La vista de aprendizaje no contiene respuestas: PublicQuestion no tiene campos para ellas.
	view, err := s.Course(ctx, "worker", org, courseID)
	if err != nil || len(view.Questions) != 3 || view.Progress.Status != domain.ProgressNotStarted {
		t.Fatal(view, err)
	}

	// DIAGNOSTIC no completa; FINAL en el límite (2/3 = 67) aprueba y emite una constancia.
	diagnostic, err := s.SubmitAttempt(ctx, "worker", org, courseID, attempt(versionID, uid(7, 1), uid(8, 1), "DIAGNOSTIC", 1, 1, 1))
	if err != nil || diagnostic.Progress.Status != domain.ProgressInProgress || diagnostic.Certificate != nil {
		t.Fatal(diagnostic, err)
	}
	final := attempt(versionID, uid(7, 2), uid(8, 2), "FINAL", 1, 1, 0)
	first, err := s.SubmitAttempt(ctx, "worker", org, courseID, final)
	if err != nil || !first.Passed || first.Score != 67 || first.Certificate == nil || first.Progress.ExperiencePoints != 20 || first.Replayed {
		t.Fatal(first, err)
	}
	if len(first.Certificate.VerificationCode) < 16 {
		t.Fatal("código de verificación débil")
	}
	replay, err := s.SubmitAttempt(ctx, "worker", org, courseID, final)
	if err != nil || !replay.Replayed || replay.Score != first.Score || len(replay.Results) != 3 || replay.Certificate.ID != first.Certificate.ID {
		t.Fatal("el replay debe devolver el mismo resultado", replay, err)
	}
	conflict := attempt(versionID, uid(7, 2), uid(8, 2), "FINAL", 1, 0, 0)
	if _, err = s.SubmitAttempt(ctx, "worker", org, courseID, conflict); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatal("contenido distinto con la misma operación", err)
	}
	if _, err = s.SubmitAttempt(ctx, "chef", org, courseID, final); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatal("otro usuario no reutiliza el intento", err)
	}
	if _, err = s.SubmitAttempt(ctx, "super", org, courseID, attempt(versionID, uid(7, 3), uid(8, 3), "FINAL", 1, 1, 1)); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("SUPER_ADMIN sin membresía no persiste intentos", err)
	}
	var attempts, answers, certificates int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM training_attempts),(SELECT count(*) FROM training_attempt_answers),(SELECT count(*) FROM training_certificates)`).Scan(&attempts, &answers, &certificates); err != nil || attempts != 2 || answers != 6 || certificates != 1 {
		t.Fatal("conteos inesperados", attempts, answers, certificates, err)
	}

	// Concurrencia: la misma operación enviada en paralelo se registra una sola vez.
	var wg sync.WaitGroup
	parallel := attempt(versionID, uid(7, 4), uid(8, 4), "DIAGNOSTIC", 1, 1, 1)
	outcomes := make([]error, 6)
	for i := range outcomes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, outcomes[i] = s.SubmitAttempt(ctx, "chef", org, courseID, parallel)
		}()
	}
	wg.Wait()
	for _, e := range outcomes {
		if e != nil {
			t.Fatal("los reintentos concurrentes idénticos deben tener éxito", e)
		}
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM training_attempts WHERE client_operation_id=$1`, uid(8, 4)).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("el intento paralelo se duplicó", attempts, err)
	}

	// Versión antigua tras publicar una nueva; la constancia anterior no cambia.
	draft, err := s.Draft(ctx, "admin", org, courseID)
	if err != nil || draft.Version.Number != 2 || len(draft.Lessons) != 1 || draft.Lessons[0].ID == uid(3, 1) {
		t.Fatal("el borrador debe copiar la versión publicada con ids nuevos", draft, err)
	}
	if _, err = s.SaveDraft(ctx, "admin", org, courseID, domain.DraftInput{PassingScore: 90, RowVersion: draft.Version.RowVersion + 5, Lessons: draft.Lessons, Questions: draft.Questions}); !errors.Is(err, domain.ErrDraftConflict) {
		t.Fatal("rowVersion antiguo", err)
	}
	if _, err = s.Publish(ctx, "admin", org, courseID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitAttempt(ctx, "worker", org, courseID, attempt(versionID, uid(7, 5), uid(8, 5), "FINAL", 1, 1, 1)); !errors.Is(err, domain.ErrVersionOutdated) {
		t.Fatal("versión antigua", err)
	}
	var published, archived, drafts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='PUBLISHED'),count(*) FILTER (WHERE status='ARCHIVED'),count(*) FILTER (WHERE status='DRAFT') FROM training_course_versions WHERE course_id=$1`, courseID).Scan(&published, &archived, &drafts); err != nil || published != 1 || archived != 1 || drafts != 0 {
		t.Fatal("estados de versión", published, archived, drafts, err)
	}
	replayLate, err := s.SubmitAttempt(ctx, "worker", org, courseID, final)
	if err != nil || !replayLate.Replayed {
		t.Fatal("un reintento tardío debe devolver el intento original", err)
	}

	// Índices V8: una sola versión DRAFT por curso y una sola constancia por (org, usuario, versión).
	if _, err = pool.Exec(ctx, `INSERT INTO training_course_versions(organization_id,course_id,version_number,status) VALUES($1,$2,10,'DRAFT'),($1,$2,11,'DRAFT')`, org, courseID); err == nil {
		t.Fatal("V8 debe impedir dos borradores")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO training_certificates(organization_id,user_id,course_version_id,final_attempt_id,verification_code) VALUES($1,'worker',$2,$3,'DUPLICATE-CODE-0000000')`, org, versionID, uid(7, 2)); err == nil {
		t.Fatal("V8 debe impedir constancias duplicadas")
	}
}

func TestTrainingPostgresVideosAndPublicationGate(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	body := []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	hash := strings.Repeat("a", 64)
	storage := videos{inspection: contracts.VideoInspection{ContentType: "video/mp4", ContentLength: 100, SHA256Hex: hash, Header: body}}
	s := application.New(New(pool), storage, domain.VideoPolicy{MaxBytes: 1 << 20})
	upload := domain.VideoUploadInput{ID: uid(5, 1), OriginalName: "clase.mp4", MIMEType: "video/mp4", SizeBytes: 100, SHA256: hash}
	reserved, err := s.ReserveVideo(ctx, "admin", org, upload)
	if err != nil || reserved.File.Status != "PENDING_UPLOAD" || reserved.Upload == nil || reserved.File.ObjectKey != "organizations/"+org+"/training/videos/"+uid(5, 1)+".mp4" {
		t.Fatal(reserved, err)
	}
	if again, err := s.ReserveVideo(ctx, "admin", org, upload); err != nil || again.Upload == nil {
		t.Fatal("la repetición devuelve la misma reserva", err)
	}
	upload.SizeBytes = 101
	if _, err = s.ReserveVideo(ctx, "admin", org, upload); !errors.Is(err, domain.ErrVideoConflict) {
		t.Fatal("metadatos distintos", err)
	}
	// Otra organización no puede apropiarse del mismo id ni confirmarlo.
	upload.SizeBytes = 100
	if _, err = s.ReserveVideo(ctx, "other", foreign, upload); !errors.Is(err, domain.ErrVideoConflict) {
		t.Fatal("el id pertenece a otra organización", err)
	}
	if _, err = s.ConfirmVideo(ctx, "other", foreign, uid(5, 1)); !errors.Is(err, domain.ErrVideoNotFound) {
		t.Fatal("confirmar un video ajeno debe ser 404", err)
	}
	if _, err = s.ConfirmVideo(ctx, "chef", org, uid(5, 1)); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("solo ADMIN confirma", err)
	}

	// Con el video PENDING se puede guardar el borrador, pero no publicar.
	course, _ := s.CreateCourse(ctx, "admin", org, domain.NewCourse{Code: "V-01", Title: "Con video"})
	draft, err := s.Draft(ctx, "admin", org, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.DraftInput{PassingScore: 80, RowVersion: draft.Version.RowVersion,
		Lessons:   []domain.Lesson{{ID: uid(3, 1), Title: "L", DisplayOrder: 1, VideoMediaID: uid(5, 1)}},
		Questions: []domain.Question{{ID: uid(2, 1), Text: "Q", Options: []string{"a", "b"}, CorrectOption: 0, DisplayOrder: 1}}}
	if _, err = s.SaveDraft(ctx, "admin", org, course.ID, in); err != nil {
		t.Fatal(err)
	}
	var incomplete *domain.IncompleteError
	if _, err = s.Publish(ctx, "admin", org, course.ID); !errors.As(err, &incomplete) {
		t.Fatal("un video pendiente bloquea la publicación", err)
	}
	confirmed, err := s.ConfirmVideo(ctx, "admin", org, uid(5, 1))
	if err != nil || confirmed.Status != "AVAILABLE" {
		t.Fatal(confirmed, err)
	}
	if _, err = s.Publish(ctx, "admin", org, course.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.Course(ctx, "worker", org, course.ID)
	if err != nil || view.Lessons[0].Video == nil || view.Lessons[0].Video.MIMEType != "video/mp4" {
		t.Fatal(view, err)
	}
	access, err := s.LessonVideo(ctx, "worker", org, course.ID, uid(3, 1))
	if err != nil || access.URL == "" {
		t.Fatal(access, err)
	}
	if _, err = s.LessonVideo(ctx, "other", foreign, course.ID, uid(3, 1)); !errors.Is(err, domain.ErrCourseNotFound) {
		t.Fatal("video de otro tenant", err)
	}
}
