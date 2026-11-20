package postgres

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

func TestLessonProgressPercentAndAssignmentsOnPostgres(t *testing.T) {
	ctx, pool := database(t)
	seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c := publishPlus(t, ctx, s, "P-01", 70, nil)
	if _, err := s.CreateAssignment(ctx, "admin", org, newAssignment(1, c.ID, domain.AssignmentTarget{Type: domain.TargetRole, Role: "WORKER"}, nil)); err != nil {
		t.Fatal(err)
	}
	percent := func() int {
		page, err := s.MyAssignments(ctx, "worker", org, pagination.Request{})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		return page.Items[0].Percent
	}
	if p := percent(); p != 0 {
		t.Fatal("sin avance", p)
	}

	// Completar una lección es idempotente y cuenta una sola vez.
	for i := 0; i < 2; i++ {
		p, err := s.SaveProgress(ctx, "worker", org, c.ID, c.LessonA, c.LessonA)
		if err != nil || p.CompletedLessons() != 1 || p.TotalLessons != 2 || p.Percent() != 50 || !reflect.DeepEqual(p.CompletedLessonIDs, []string{c.LessonA}) {
			t.Fatal(i, p, err)
		}
	}
	if p := percent(); p != 50 {
		t.Fatal("una de dos", p)
	}
	// Lección ajena a la versión publicada (otro curso o inexistente): no se marca.
	other := publishPlus(t, ctx, s, "P-02", 80, nil)
	for _, lesson := range []string{other.LessonA, uid(99, 99)} {
		if _, err := s.SaveProgress(ctx, "worker", org, c.ID, c.LessonA, lesson); !errors.Is(err, domain.ErrLessonNotFound) {
			t.Fatal("lección fuera de la versión", err)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM training_lesson_progress`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("el rechazo no deja marcas", rows, err)
	}

	// MODULE aprobado marca la lección con origen MODULE_PASSED, y una vista posterior no lo degrada.
	module := plusAttempt(c, 1, "MODULE", c.LessonB, []int{3}, 1)
	out, err := s.SubmitAttempt(ctx, "worker", org, c.ID, module)
	if err != nil || out.Progress.CompletedLessons() != 2 || out.Progress.Percent() != 100 {
		t.Fatal(out.Progress, err)
	}
	if replay, e := s.SubmitAttempt(ctx, "worker", org, c.ID, module); e != nil || !replay.Replayed || replay.Progress.CompletedLessons() != 2 {
		t.Fatal("replay", replay.Progress, e)
	}
	if _, err = s.SaveProgress(ctx, "worker", org, c.ID, c.LessonB, c.LessonB); err != nil {
		t.Fatal(err)
	}
	var source string
	if err = pool.QueryRow(ctx, `SELECT source FROM training_lesson_progress WHERE lesson_id=$1`, c.LessonB).Scan(&source); err != nil || source != "MODULE_PASSED" {
		t.Fatal("origen", source, err)
	}
	// Un módulo reprobado no marca nada.
	if _, err = pool.Exec(ctx, `DELETE FROM training_lesson_progress WHERE lesson_id=$1`, c.LessonB); err != nil {
		t.Fatal(err)
	}
	if bad, e := s.SubmitAttempt(ctx, "worker", org, c.ID, plusAttempt(c, 2, "MODULE", c.LessonB, []int{3}, 0)); e != nil || bad.Progress.CompletedLessons() != 1 {
		t.Fatal("módulo reprobado", bad.Progress, e)
	}

	// El curso COMPLETED vale 100 % aunque no tenga marcas por lección.
	if _, err = pool.Exec(ctx, `DELETE FROM training_lesson_progress`); err != nil {
		t.Fatal(err)
	}
	final, err := s.SubmitAttempt(ctx, "worker", org, c.ID, plusAttempt(c, 3, "FINAL", "", []int{1, 2, 3}, 1, 1, 1))
	if err != nil || final.Progress.Status != domain.ProgressCompleted || final.Progress.Percent() != 100 || final.Progress.CompletedLessons() != 2 {
		t.Fatal(final.Progress, err)
	}
	if p := percent(); p != 100 {
		t.Fatal("completado", p)
	}
	view, err := s.Course(ctx, "worker", org, c.ID)
	if err != nil || view.Progress.Percent() != 100 || view.Progress.TotalLessons != 2 {
		t.Fatal(view.Progress, err)
	}
	// Otra persona de la empresa no hereda el avance.
	other2, err := s.Course(ctx, "chef", org, c.ID)
	if err != nil || other2.Progress.CompletedLessons() != 0 || other2.Progress.Percent() != 0 {
		t.Fatal(other2.Progress, err)
	}
	// El catálogo offline no lleva datos de la persona.
	catalog, err := s.OfflineCatalog(ctx, "worker", org)
	if err != nil {
		t.Fatal(err)
	}
	for _, course := range catalog.Courses {
		if course.Progress.TotalLessons != 0 || len(course.Progress.CompletedLessonIDs) != 0 {
			t.Fatal("el offline no debe traer progreso", course.Progress)
		}
	}
}

func TestKeyPointsPersistKeepAbsentAndReachLearnerOnPostgres(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	course, err := s.CreateCourse(ctx, "admin", org, domain.NewCourse{Code: "K-01", Title: "Puntos"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.Draft(ctx, "admin", org, course.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, b := uid(3, 1), uid(3, 2)
	question := domain.Question{ID: uid(2, 1), LessonID: a, Text: "Q", Options: []string{"a", "b"}, CorrectOption: 0, DisplayOrder: 1}
	save := func(rowVersion int, first, second []string) domain.Draft {
		t.Helper()
		out, e := s.SaveDraft(ctx, "admin", org, course.ID, domain.DraftInput{PassingScore: 80, RowVersion: rowVersion, Questions: []domain.Question{question},
			Lessons: []domain.Lesson{{ID: a, Title: "A", DisplayOrder: 1, KeyPoints: first}, {ID: b, Title: "B", DisplayOrder: 2, KeyPoints: second}}})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	d := save(draft.Version.RowVersion, []string{" Lavar ", "Secar"}, nil)
	if !reflect.DeepEqual(d.Lessons[0].KeyPoints, []string{"Lavar", "Secar"}) || d.Lessons[1].KeyPoints == nil || len(d.Lessons[1].KeyPoints) != 0 {
		t.Fatal(d.Lessons)
	}
	// Ausente conserva, [] borra: se comprueba contra la base y no solo contra lo devuelto.
	d = save(d.Version.RowVersion, nil, []string{"Uno"})
	var raw string
	if err = pool.QueryRow(ctx, `SELECT key_points::text FROM training_lessons WHERE id=$1`, a).Scan(&raw); err != nil || raw != `["Lavar", "Secar"]` {
		t.Fatal("ausente conserva", raw, err)
	}
	d = save(d.Version.RowVersion, []string{}, nil)
	if err = pool.QueryRow(ctx, `SELECT key_points::text FROM training_lessons WHERE id=$1`, a).Scan(&raw); err != nil || raw != `[]` {
		t.Fatal("[] borra", raw, err)
	}
	if err = pool.QueryRow(ctx, `SELECT key_points::text FROM training_lessons WHERE id=$1`, b).Scan(&raw); err != nil || raw != `["Uno"]` {
		t.Fatal("la otra lección conserva", raw, err)
	}
	save(d.Version.RowVersion, []string{"Final"}, nil)
	// La base rechaza más de 5 puntos aunque el dominio se omita.
	if _, err = pool.Exec(ctx, `UPDATE training_lessons SET key_points='["1","2","3","4","5","6"]'::jsonb WHERE id=$1`, a); err == nil {
		t.Fatal("el CHECK debe limitar a 5 puntos")
	}
	if _, err = s.Publish(ctx, "admin", org, course.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.Course(ctx, "worker", org, course.ID)
	if err != nil || !reflect.DeepEqual(view.Lessons[0].KeyPoints, []string{"Final"}) || view.Lessons[1].KeyPoints == nil || len(view.Lessons[1].KeyPoints) != 1 {
		t.Fatal(view.Lessons, err)
	}
	next, err := s.Draft(ctx, "admin", org, course.ID)
	if err != nil || !reflect.DeepEqual(next.Lessons[0].KeyPoints, []string{"Final"}) {
		t.Fatal("el borrador siguiente copia los puntos", next.Lessons, err)
	}
}
