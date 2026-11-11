package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5/pgxpool"
)

// plusCourse publica un curso con dos lecciones: la A (base+1) con dos preguntas y
// la B (base+2) con una; todas con respuesta correcta 1 y mínimo de aprobación 67.
// Los identificadores usan la familia base para poder publicar varios cursos.
type plusCourse struct {
	ID, VersionID, LessonA, LessonB string
	Base                            int
}

func qid(c plusCourse, i int) string { return uid(c.Base, 100+i) }

func publishPlus(t *testing.T, ctx context.Context, s *application.Service, code string, base int, external *domain.ExternalVideo) plusCourse {
	t.Helper()
	course, err := s.CreateCourse(ctx, "admin", org, domain.NewCourse{Code: code, Title: "Curso " + code})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.Draft(ctx, "admin", org, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := plusCourse{ID: course.ID, LessonA: uid(base, 1), LessonB: uid(base, 2), Base: base}
	in := domain.DraftInput{PassingScore: 67, RowVersion: draft.Version.RowVersion, Lessons: []domain.Lesson{
		{ID: c.LessonA, Title: "A", Content: "a", DisplayOrder: 1, ExternalVideo: external}, {ID: c.LessonB, Title: "B", Content: "b", DisplayOrder: 2}}}
	for i, lesson := range []string{c.LessonA, c.LessonA, c.LessonB} {
		in.Questions = append(in.Questions, domain.Question{ID: qid(c, i+1), LessonID: lesson, Text: "Q", Options: []string{"a", "b", "c"}, CorrectOption: 1, Explanation: "e", DisplayOrder: i + 1})
	}
	if _, err = s.SaveDraft(ctx, "admin", org, course.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "admin", org, course.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.Course(ctx, "worker", org, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.VersionID = view.Version.ID
	return c
}

// plusAttempt arma un intento; picks es la opción por pregunta de ids (en orden).
func plusAttempt(c plusCourse, n int, kind, lesson string, questions []int, picks ...int) domain.AttemptInput {
	in := domain.AttemptInput{ID: uid(60, n), ClientOperationID: uid(61, n), VersionID: c.VersionID, AttemptType: kind, LessonID: lesson,
		StartedAt: time.Now().Add(-time.Minute), ClientCreatedAt: time.Now()}
	for i, q := range questions {
		in.Answers = append(in.Answers, domain.Answer{QuestionID: qid(c, q), SelectedOption: picks[i]})
	}
	return in
}

func seedPlus(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (location, foreignLocation string) {
	t.Helper()
	seed(t, ctx, pool)
	location, foreignLocation = uid(40, 1), uid(40, 2)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO locations(id,organization_id,name,address) VALUES($1,$2,'Sede','Calle 1'),($3,$4,'Ajena','Calle 2')`, []any{location, org, foreignLocation, foreign}},
		{`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2)`, []any{org, location}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func newAssignment(n int, course string, target domain.AssignmentTarget, due *time.Time) domain.NewAssignment {
	return domain.NewAssignment{ID: uid(50, n), CourseID: course, Target: target, DueAt: due}
}

func TestAssignmentsTenantRolesDueDatesAndAlertRule(t *testing.T) {
	ctx, pool := database(t)
	location, foreignLocation := seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c1 := publishPlus(t, ctx, s, "A-01", 20, nil)
	draftOnly, err := s.CreateCourse(ctx, "admin", org, domain.NewCourse{Code: "A-02", Title: "Borrador"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO training_courses(id,organization_id,code,title,status) VALUES($1,$2,'F-01','Ajeno','PUBLISHED')`, uid(41, 1), foreign); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(48 * time.Hour)
	worker := domain.AssignmentTarget{Type: domain.TargetUser, UserID: "worker"}

	// Rol sin permiso: solo ADMIN asigna; un administrador de otra empresa tampoco.
	for _, user := range []string{"worker", "chef", "other"} {
		if _, err = s.CreateAssignment(ctx, user, org, newAssignment(1, c1.ID, worker, &due)); !errors.Is(err, domain.ErrDenied) {
			t.Fatal(user, "no debe asignar", err)
		}
	}
	// Otra empresa: curso, persona y sede ajenos responden como inexistentes.
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(2, uid(41, 1), worker, nil)); !errors.Is(err, domain.ErrCourseNotFound) {
		t.Fatal("curso ajeno", err)
	}
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(3, c1.ID, domain.AssignmentTarget{Type: domain.TargetUser, UserID: "other"}, nil)); !errors.Is(err, domain.ErrAssigneeNotFound) {
		t.Fatal("persona ajena", err)
	}
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(4, c1.ID, domain.AssignmentTarget{Type: domain.TargetLocation, LocationID: foreignLocation}, nil)); !errors.Is(err, domain.ErrLocationNotFound) {
		t.Fatal("sede ajena", err)
	}
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(5, draftOnly.ID, worker, nil)); !errors.Is(err, domain.ErrCourseNotPublished) {
		t.Fatal("curso sin publicar", err)
	}
	past := time.Now().Add(-time.Hour)
	var validation *domain.ValidationError
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(6, c1.ID, worker, &past)); !errors.As(err, &validation) {
		t.Fatal("fecha pasada", err)
	}

	created, err := s.CreateAssignment(ctx, "admin", org, newAssignment(7, c1.ID, worker, &due))
	if err != nil || created.Replayed || created.Assignment.Status != domain.AssignmentActive || created.Assignment.UserName != "Worker" {
		t.Fatal(created, err)
	}
	var rules int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM alert_rules WHERE organization_id=$1 AND alert_type='TRAINING_ASSIGNED' AND active AND location_id IS NULL`, org).Scan(&rules); err != nil || rules != 1 {
		t.Fatal("debe existir una regla TRAINING_ASSIGNED activa", rules, err)
	}
	if replay, e := s.CreateAssignment(ctx, "admin", org, newAssignment(7, c1.ID, worker, &due)); e != nil || !replay.Replayed {
		t.Fatal("repetición idéntica", replay, e)
	}
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(7, c1.ID, domain.AssignmentTarget{Type: domain.TargetUser, UserID: "chef"}, &due)); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatal("mismo id con otro contenido", err)
	}
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(8, c1.ID, worker, nil)); !errors.Is(err, domain.ErrAssignmentExists) {
		t.Fatal("duplicado activo", err)
	}
	if _, err = pool.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	// La regla no se duplica al crear más asignaciones.
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(9, c1.ID, domain.AssignmentTarget{Type: domain.TargetRole, Role: "CHEF", LocationID: location}, nil)); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM alert_rules WHERE organization_id=$1 AND alert_type='TRAINING_ASSIGNED'`, org).Scan(&rules); err != nil || rules != 1 {
		t.Fatal("regla duplicada", rules, err)
	}

	// Mis capacitaciones: estado calculado desde el progreso y el vencimiento.
	mine := func(user string) []domain.MyAssignment {
		page, e := s.MyAssignments(ctx, user, org, pagination.Request{})
		if e != nil {
			t.Fatal(e)
		}
		return page.Items
	}
	if items := mine("worker"); len(items) != 1 || items[0].Status != domain.LearnPending {
		t.Fatal("pendiente", items)
	}
	if items := mine("chef"); len(items) != 1 {
		t.Fatal("el rol CHEF en la sede lo ve", items)
	}
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 1, "DIAGNOSTIC", "", []int{1, 2, 3}, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if items := mine("worker"); items[0].Status != domain.LearnInProgress {
		t.Fatal("en curso", items)
	}
	if _, err = pool.Exec(ctx, `UPDATE training_assignments SET due_at=NOW()-interval '1 day' WHERE id=$1`, uid(50, 7)); err != nil {
		t.Fatal(err)
	}
	if items := mine("worker"); items[0].Status != domain.LearnOverdue {
		t.Fatal("vencida", items)
	}
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 2, "FINAL", "", []int{1, 2, 3}, 1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if items := mine("worker"); items[0].Status != domain.LearnCompleted {
		t.Fatal("completada aunque tardía", items)
	}

	// Listado de administración con paginación y cancelación idempotente.
	page, err := s.ListAssignments(ctx, "admin", org, domain.AssignmentFilter{}, pagination.Request{Size: 1})
	if err != nil || len(page.Items) != 1 || page.NextToken == "" {
		t.Fatal(page, err)
	}
	next, err := s.ListAssignments(ctx, "admin", org, domain.AssignmentFilter{}, pagination.Request{Size: 1, Token: page.NextToken})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("segunda página", next, err)
	}
	if _, err = s.ListAssignments(ctx, "worker", org, domain.AssignmentFilter{}, pagination.Request{}); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("el trabajador no lista", err)
	}
	if _, err = s.CancelAssignment(ctx, "worker", org, uid(50, 7)); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("el trabajador no cancela", err)
	}
	if _, err = s.CancelAssignment(ctx, "other", org, uid(50, 7)); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("admin ajeno no cancela", err)
	}
	if _, err = s.CancelAssignment(ctx, "admin", org, uid(50, 99)); !errors.Is(err, domain.ErrAssignmentNotFound) {
		t.Fatal("inexistente", err)
	}
	for i := 0; i < 2; i++ {
		cancelled, e := s.CancelAssignment(ctx, "admin", org, uid(50, 7))
		if e != nil || cancelled.Status != domain.AssignmentCancelled || cancelled.CancelledAt == nil {
			t.Fatal(cancelled, e)
		}
	}
	if items := mine("worker"); len(items) != 0 {
		t.Fatal("cancelada no aparece", items)
	}
	// Tras cancelar puede volver a asignarse.
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(10, c1.ID, worker, nil)); err != nil {
		t.Fatal(err)
	}
}

func TestAchievementsAreAwardedOnceAndModuleAttemptsStayScoped(t *testing.T) {
	ctx, pool := database(t)
	seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c1 := publishPlus(t, ctx, s, "L-01", 20, nil)
	c2 := publishPlus(t, ctx, s, "L-02", 30, nil)

	// MODULE solo evalúa las preguntas de su lección y no completa el curso.
	module := plusAttempt(c1, 1, "MODULE", c1.LessonA, []int{1, 2}, 1, 1)
	out, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, module)
	if err != nil || out.Score != 100 || !out.Passed || out.Progress.Status != domain.ProgressInProgress || out.Certificate != nil || len(out.Results) != 2 {
		t.Fatal(out, err)
	}
	if len(out.Achievements) != 1 || out.Achievements[0].Code != "PERFECT_SCORE" {
		t.Fatal("el módulo perfecto da la insignia de puntaje perfecto", out.Achievements)
	}
	replay, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, module)
	if err != nil || !replay.Replayed || len(replay.Achievements) != 1 {
		t.Fatal("la repetición devuelve el mismo resultado e insignias", replay, err)
	}
	var awarded, certificates int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM user_achievements),(SELECT count(*) FROM training_certificates)`).Scan(&awarded, &certificates); err != nil || awarded != 1 || certificates != 0 {
		t.Fatal("conteos", awarded, certificates, err)
	}
	// Lección ajena (de otro curso o inexistente) y respuestas de otra lección.
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 2, "MODULE", c2.LessonA, []int{1, 2}, 1, 1)); !errors.Is(err, domain.ErrLessonNotFound) {
		t.Fatal("lección de otro curso", err)
	}
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 3, "MODULE", uid(99, 99), []int{1, 2}, 1, 1)); !errors.Is(err, domain.ErrLessonNotFound) {
		t.Fatal("lección inexistente", err)
	}
	var answers *domain.AnswersError
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 4, "MODULE", c1.LessonA, []int{1, 2, 3}, 1, 1, 1)); !errors.As(err, &answers) {
		t.Fatal("pregunta de otra lección", err)
	}
	var validation *domain.ValidationError
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 5, "MODULE", "", []int{1, 2}, 1, 1)); !errors.As(err, &validation) {
		t.Fatal("MODULE sin lección", err)
	}
	// Reintentar el módulo con otro id no otorga ni XP ni insignias nuevas.
	again, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 6, "MODULE", c1.LessonA, []int{1, 2}, 1, 1))
	if err != nil || len(again.Achievements) != 0 {
		t.Fatal(again, err)
	}
	// Un FINAL perfecto completa el curso (FIRST_COURSE); otro FINAL no repite nada.
	final, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 7, "FINAL", "", []int{1, 2, 3}, 1, 1, 1))
	if err != nil || final.Certificate == nil || len(final.Achievements) != 1 || final.Achievements[0].Code != "FIRST_COURSE" {
		t.Fatal(final, err)
	}
	last, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 8, "FINAL", "", []int{1, 2, 3}, 1, 1, 1))
	if err != nil || len(last.Achievements) != 0 {
		t.Fatal("no se repite", last, err)
	}
	mine, err := s.MyAchievements(ctx, "worker", org)
	if err != nil {
		t.Fatal(err)
	}
	earned := 0
	for _, a := range mine.Items {
		if a.AwardedAt != nil {
			earned++
		}
	}
	// XP: curso 30 (3 correctas x 10) + módulo 10 (2 x 5) + insignias 30 + 20.
	if earned != 2 || mine.Totals.Course != 30 || mine.Totals.Module != 10 || mine.Totals.Achievements != 50 || mine.Totals.Total() != 90 {
		t.Fatal(mine)
	}
	if _, err = s.MyAchievements(ctx, "super", org); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("SUPER_ADMIN no tiene membresía", err)
	}
	// Administración del catálogo: solo ADMIN, solo de su empresa.
	if _, err = s.AchievementCatalog(ctx, "worker", org); !errors.Is(err, domain.ErrDenied) {
		t.Fatal(err)
	}
	catalog, err := s.AchievementCatalog(ctx, "admin", org)
	if err != nil || len(catalog) != 4 {
		t.Fatal(catalog, err)
	}
	edited, err := s.UpdateAchievement(ctx, "admin", org, catalog[0].ID, domain.AchievementUpdate{Name: "Nuevo nombre", Points: 5, Threshold: 2, Active: false})
	if err != nil || edited.Active || edited.Points != 5 {
		t.Fatal(edited, err)
	}
	if _, err = s.UpdateAchievement(ctx, "other", foreign, catalog[0].ID, domain.AchievementUpdate{Name: "Otro", Points: 5, Threshold: 2, Active: true}); !errors.Is(err, domain.ErrAchievementNotFound) {
		t.Fatal("insignia de otra empresa", err)
	}
}

func TestPublicCertificateVerificationOfflineCatalogAndExternalVideo(t *testing.T) {
	ctx, pool := database(t)
	seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	external := &domain.ExternalVideo{Provider: domain.ProviderYouTube, VideoID: "dQw4w9WgXcQ"}
	c1 := publishPlus(t, ctx, s, "V-01", 20, external)

	// Video externo: visible al aprender; y excluyente con el video privado (422).
	view, err := s.Course(ctx, "worker", org, c1.ID)
	if err != nil || view.Lessons[0].ExternalVideo == nil || view.Lessons[0].ExternalVideo.VideoID != "dQw4w9WgXcQ" || view.Lessons[1].ExternalVideo != nil {
		t.Fatal(view, err)
	}
	draft, err := s.Draft(ctx, "admin", org, c1.ID)
	if err != nil || draft.Lessons[0].ExternalVideo == nil {
		t.Fatal("el borrador copia el video externo", draft, err)
	}
	both := domain.DraftInput{PassingScore: 67, RowVersion: draft.Version.RowVersion, Questions: draft.Questions,
		Lessons: []domain.Lesson{{ID: uid(21, 1), Title: "x", DisplayOrder: 1, VideoMediaID: uid(21, 9), ExternalVideo: external}}}
	var validation *domain.ValidationError
	if _, err = s.SaveDraft(ctx, "admin", org, c1.ID, both); !errors.As(err, &validation) || validation.Fields["lessons[0].externalVideo"] == "" {
		t.Fatal("video privado y externo a la vez", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE training_lessons SET media_file_id=NULL WHERE id=$1`, c1.LessonB); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE training_lessons SET external_video_id='corto' WHERE id=$1`, c1.LessonB); err == nil {
		t.Fatal("la base acepta un identificador de YouTube inválido")
	}

	// Catálogo offline: solo lo asignado, con etag estable y sin respuestas ni URL.
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(1, c1.ID, domain.AssignmentTarget{Type: domain.TargetUser, UserID: "worker"}, nil)); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.OfflineCatalog(ctx, "worker", org)
	if err != nil || len(catalog.Courses) != 1 || catalog.ETag == "" || len(catalog.Courses[0].Questions) != 3 {
		t.Fatal(catalog, err)
	}
	again, _ := s.OfflineCatalog(ctx, "worker", org)
	if again.ETag != catalog.ETag {
		t.Fatal("el etag debe ser estable")
	}
	if empty, e := s.OfflineCatalog(ctx, "chef", org); e != nil || len(empty.Courses) != 0 || empty.ETag == catalog.ETag {
		t.Fatal("el paquete es por usuario", empty, e)
	}
	c2 := publishPlus(t, ctx, s, "V-02", 30, nil)
	if _, err = s.CreateAssignment(ctx, "admin", org, newAssignment(2, c2.ID, domain.AssignmentTarget{Type: domain.TargetRole, Role: "WORKER"}, nil)); err != nil {
		t.Fatal(err)
	}
	if changed, e := s.OfflineCatalog(ctx, "worker", org); e != nil || changed.ETag == catalog.ETag || len(changed.Courses) != 2 {
		t.Fatal("el etag cambia con el contenido", changed, e)
	}

	// Verificación pública: inexistente, formato inválido, vigente y revocada.
	final, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 1, "FINAL", "", []int{1, 2, 3}, 1, 1, 1))
	if err != nil || final.Certificate == nil {
		t.Fatal(final, err)
	}
	code := final.Certificate.VerificationCode
	for _, bad := range []string{"AAAAAAAAAAAAAAAAAAAAAAAA", "corto", "", code + "A", "' OR 1=1 --"} {
		if _, err = s.VerifyCertificate(ctx, bad); !errors.Is(err, domain.ErrCertificateNotFound) {
			t.Fatal("código inexistente o inválido", bad, err)
		}
	}
	valid, err := s.VerifyCertificate(ctx, code)
	if err != nil || valid.Status != domain.CertificateValid || valid.CourseTitle != "Curso V-01" || valid.VersionNumber != 1 || valid.Organization != "Organización" || valid.Holder != "W." {
		t.Fatal(valid, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE training_certificates SET revoked_at=NOW() WHERE verification_code=$1`, code); err != nil {
		t.Fatal(err)
	}
	if revoked, e := s.VerifyCertificate(ctx, code); e != nil || revoked.Status != domain.CertificateRevoked {
		t.Fatal("revocada", revoked, e)
	}
}

func TestLearningPathsLifecycleProgressAndTenant(t *testing.T) {
	ctx, pool := database(t)
	seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c1 := publishPlus(t, ctx, s, "R-01", 20, nil)
	c2 := publishPlus(t, ctx, s, "R-02", 30, nil)
	draftCourse, err := s.CreateCourse(ctx, "admin", org, domain.NewCourse{Code: "R-03", Title: "Sin publicar"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO training_courses(id,organization_id,code,title,status) VALUES($1,$2,'F-01','Ajeno','PUBLISHED')`, uid(41, 1), foreign); err != nil {
		t.Fatal(err)
	}
	in := domain.PathInput{ID: uid(70, 1), Title: "Inducción", Description: "Ruta", CourseIDs: []string{c1.ID, c2.ID}}
	if _, err = s.CreatePath(ctx, "worker", org, in); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("el trabajador no crea rutas", err)
	}
	foreignIn := in
	foreignIn.CourseIDs = []string{uid(41, 1)}
	var validation *domain.ValidationError
	if _, err = s.CreatePath(ctx, "admin", org, foreignIn); !errors.As(err, &validation) {
		t.Fatal("curso ajeno en la ruta", err)
	}
	created, err := s.CreatePath(ctx, "admin", org, in)
	if err != nil || created.Path.Status != domain.PathDraft || len(created.Path.Courses) != 2 || created.Path.Courses[0].CourseID != c1.ID {
		t.Fatal(created, err)
	}
	if again, e := s.CreatePath(ctx, "admin", org, in); e != nil || !again.Replayed {
		t.Fatal(again, e)
	}
	changed := in
	changed.Title = "Otro"
	if _, err = s.CreatePath(ctx, "admin", org, changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	// Una ruta en borrador no la ven quienes aprenden.
	if _, err = s.Path(ctx, "worker", org, in.ID); !errors.Is(err, domain.ErrPathNotFound) {
		t.Fatal("borrador oculto", err)
	}
	// Edición con control de versión; publicar exige cursos publicados.
	edit := domain.PathInput{Title: "Inducción 2", CourseIDs: []string{c2.ID, c1.ID, draftCourse.ID}, RowVersion: 99}
	if _, err = s.UpdatePath(ctx, "admin", org, in.ID, edit); !errors.Is(err, domain.ErrPathConflict) {
		t.Fatal("versión vieja", err)
	}
	edit.RowVersion = 1
	updated, err := s.UpdatePath(ctx, "admin", org, in.ID, edit)
	if err != nil || updated.Path.RowVersion != 2 || updated.Path.Courses[0].CourseID != c2.ID {
		t.Fatal(updated, err)
	}
	var incomplete *domain.IncompleteError
	if _, err = s.PublishPath(ctx, "admin", org, in.ID); !errors.As(err, &incomplete) {
		t.Fatal("publicar con un curso sin publicar", err)
	}
	edit.RowVersion, edit.CourseIDs = 2, []string{c1.ID, c2.ID}
	if _, err = s.UpdatePath(ctx, "admin", org, in.ID, edit); err != nil {
		t.Fatal(err)
	}
	published, err := s.PublishPath(ctx, "admin", org, in.ID)
	if err != nil || published.Path.Status != domain.PathPublished || published.Path.PublishedAt == nil {
		t.Fatal(published, err)
	}
	if again, e := s.PublishPath(ctx, "admin", org, in.ID); e != nil || again.Path.RowVersion != published.Path.RowVersion {
		t.Fatal("publicar es idempotente", again, e)
	}

	// Avance: completar el primer curso deja la ruta al 50 %.
	if _, err = s.SubmitAttempt(ctx, "worker", org, c1.ID, plusAttempt(c1, 1, "FINAL", "", []int{1, 2, 3}, 1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	view, err := s.Path(ctx, "worker", org, in.ID)
	if err != nil || view.Progress.Percent != 50 || view.Progress.CompletedCourses != 1 || view.Progress.NextCourseID != c2.ID || view.Progress.Status != domain.ProgressInProgress {
		t.Fatal(view, err)
	}
	if page, e := s.ListPaths(ctx, "worker", org, "", pagination.Request{}); e != nil || len(page.Items) != 1 || page.Items[0].Progress.Percent != 50 {
		t.Fatal(page, e)
	}
	if _, err = s.ListPaths(ctx, "worker", org, domain.PathDraft, pagination.Request{}); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("el trabajador no lista borradores", err)
	}
	if _, err = s.Path(ctx, "other", org, in.ID); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("usuario ajeno", err)
	}
	if _, err = s.Path(ctx, "other", foreign, in.ID); !errors.Is(err, domain.ErrPathNotFound) {
		t.Fatal("ruta de otra empresa", err)
	}
	// Archivar retira la ruta y bloquea ediciones.
	if _, err = s.ArchivePath(ctx, "admin", org, in.ID); err != nil {
		t.Fatal(err)
	}
	edit.RowVersion = published.Path.RowVersion + 1
	if _, err = s.UpdatePath(ctx, "admin", org, in.ID, edit); !errors.Is(err, domain.ErrPathArchived) {
		t.Fatal("ruta archivada", err)
	}
	if _, err = s.Path(ctx, "worker", org, in.ID); !errors.Is(err, domain.ErrPathNotFound) {
		t.Fatal("archivada oculta", err)
	}
}
