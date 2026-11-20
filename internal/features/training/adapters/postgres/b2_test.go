package postgres

import (
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

func TestAttemptXPIsPersistedAndStableOnReplayOnPostgres(t *testing.T) {
	ctx, pool := database(t)
	seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c := publishPlus(t, ctx, s, "X-01", 20, nil)
	stored := func(n int) *int {
		var xp *int
		if err := pool.QueryRow(ctx, `SELECT xp_awarded FROM training_attempts WHERE id=$1`, uid(60, n)).Scan(&xp); err != nil {
			t.Fatal(err)
		}
		return xp
	}
	earned := func(out application.AttemptOutcome) int {
		t.Helper()
		if out.XPEarned == nil {
			t.Fatal("falta XPEarned")
		}
		return *out.XPEarned
	}

	// MODULE perfecto de la lección A: 2 correctas x 5 = 10 + PERFECT_SCORE (30).
	module := plusAttempt(c, 1, "MODULE", c.LessonA, []int{1, 2}, 1, 1)
	first, err := s.SubmitAttempt(ctx, "worker", org, c.ID, module)
	if err != nil || earned(first) != 40 || stored(1) == nil || *stored(1) != 10 {
		t.Fatal(first.XPEarned, stored(1), err)
	}
	replay, err := s.SubmitAttempt(ctx, "worker", org, c.ID, module)
	if err != nil || !replay.Replayed || earned(replay) != 40 {
		t.Fatal("replay", replay.XPEarned, err)
	}
	// Un MODULE con la misma nota no suma nada.
	again, err := s.SubmitAttempt(ctx, "worker", org, c.ID, plusAttempt(c, 2, "MODULE", c.LessonA, []int{1, 2}, 1, 1))
	if err != nil || earned(again) != 0 || *stored(2) != 0 {
		t.Fatal(again.XPEarned, stored(2), err)
	}
	// DIAGNOSTIC: 0 persistido (no NULL).
	if diag, e := s.SubmitAttempt(ctx, "worker", org, c.ID, plusAttempt(c, 3, "DIAGNOSTIC", "", []int{1, 2, 3}, 1, 1, 1)); e != nil || earned(diag) != 0 || stored(3) == nil || *stored(3) != 0 {
		t.Fatal(diag.XPEarned, stored(3), e)
	}
	// FINAL 2/3: 20 de curso + FIRST_COURSE (20).
	final := plusAttempt(c, 4, "FINAL", "", []int{1, 2, 3}, 1, 1, 0)
	out, err := s.SubmitAttempt(ctx, "worker", org, c.ID, final)
	if err != nil || earned(out) != 40 || *stored(4) != 20 {
		t.Fatal(out.XPEarned, stored(4), err)
	}
	// FINAL perfecto: +10 de curso; FINAL repetido con la misma nota: 0.
	if better, e := s.SubmitAttempt(ctx, "worker", org, c.ID, plusAttempt(c, 5, "FINAL", "", []int{1, 2, 3}, 1, 1, 1)); e != nil || earned(better) != 10 || *stored(5) != 10 {
		t.Fatal(better.XPEarned, stored(5), e)
	}
	repeated := plusAttempt(c, 6, "FINAL", "", []int{1, 2, 3}, 1, 1, 1)
	same, err := s.SubmitAttempt(ctx, "worker", org, c.ID, repeated)
	if err != nil || earned(same) != 0 || *stored(6) != 0 {
		t.Fatal(same.XPEarned, stored(6), err)
	}
	if r, e := s.SubmitAttempt(ctx, "worker", org, c.ID, repeated); e != nil || !r.Replayed || earned(r) != 0 {
		t.Fatal("replay del FINAL repetido", r.XPEarned, e)
	}
	// La suma de lo ganado por intento más insignias es el XP total del usuario.
	var courseAndModule int
	if err = pool.QueryRow(ctx, `SELECT COALESCE(sum(xp_awarded),0) FROM training_attempts WHERE user_id='worker'`).Scan(&courseAndModule); err != nil {
		t.Fatal(err)
	}
	mine, err := s.MyAchievements(ctx, "worker", org)
	if err != nil || courseAndModule+mine.Totals.Achievements != mine.Totals.Total() {
		t.Fatal(courseAndModule, mine.Totals, err)
	}
	// Intentos anteriores a V15 (xp_awarded NULL): el replay omite XPEarned.
	if _, err = pool.Exec(ctx, `UPDATE training_attempts SET xp_awarded=NULL WHERE id=$1`, uid(60, 4)); err != nil {
		t.Fatal(err)
	}
	if legacy, e := s.SubmitAttempt(ctx, "worker", org, c.ID, final); e != nil || !legacy.Replayed || legacy.XPEarned != nil {
		t.Fatal("histórico", legacy.XPEarned, e)
	}
}

func TestStreakUsesFirstActiveLocationZoneAndUnionOfActivityOnPostgres(t *testing.T) {
	ctx, pool := database(t)
	location, _ := seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c := publishPlus(t, ctx, s, "S-01", 20, nil)
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("sin base de zonas horarias")
	}
	if _, err = pool.Exec(ctx, `UPDATE locations SET timezone='Asia/Tokyo' WHERE id=$1`, location); err != nil {
		t.Fatal(err)
	}
	// El administrador de la sede (chef) tiene la zona de la sede; el trabajador sin sede, America/Lima.
	if empty, e := s.MyAchievements(ctx, "chef", org); e != nil || empty.Streak.CurrentDays != 0 || empty.Streak.Timezone != "Asia/Tokyo" || empty.Streak.LastActivityDate != "" {
		t.Fatal(empty.Streak, e)
	}
	if lima, e := s.MyAchievements(ctx, "worker", org); e != nil || lima.Streak.Timezone != domain.DefaultStreakTimezone {
		t.Fatal(lima.Streak, e)
	}
	// Una sede inactiva no cuenta: sin otra sede activa vuelve a America/Lima.
	if _, err = pool.Exec(ctx, `UPDATE locations SET status='INACTIVE' WHERE id=$1`, location); err != nil {
		t.Fatal(err)
	}
	if inactive, e := s.MyAchievements(ctx, "chef", org); e != nil || inactive.Streak.Timezone != domain.DefaultStreakTimezone {
		t.Fatal(inactive.Streak, e)
	}
	if _, err = pool.Exec(ctx, `UPDATE locations SET status='ACTIVE' WHERE id=$1`, location); err != nil {
		t.Fatal(err)
	}

	// Una lección completada (hoy) y un intento (23:00 de ayer, hora de Tokio): dos días seguidos en la
	// zona de la sede aunque ambos caigan en el mismo día UTC.
	if _, err = s.SaveProgress(ctx, "chef", org, c.ID, c.LessonA, c.LessonA); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubmitAttempt(ctx, "chef", org, c.ID, plusAttempt(c, 1, "DIAGNOSTIC", "", []int{1, 2, 3}, 1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(tokyo)
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tokyo)
	if _, err = pool.Exec(ctx, `UPDATE training_attempts SET completed_at=$1 WHERE id=$2`, startToday.Add(-time.Hour), uid(60, 1)); err != nil {
		t.Fatal(err)
	}
	got, err := s.MyAchievements(ctx, "chef", org)
	want := domain.Streak{CurrentDays: 2, LongestDays: 2, LastActivityDate: now.Format("2006-01-02"), ActiveToday: true, Timezone: "Asia/Tokyo"}
	if err != nil || got.Streak != want {
		t.Fatal(got.Streak, want, err)
	}
	// Fuera de la ventana de 400 días la actividad no se consulta: queda solo el intento de ayer (racha vigente de 1).
	if _, err = pool.Exec(ctx, `UPDATE training_lesson_progress SET completed_at=$1`, startToday.AddDate(0, 0, -(domain.StreakWindowDays+5))); err != nil {
		t.Fatal(err)
	}
	old, err := s.MyAchievements(ctx, "chef", org)
	if err != nil || old.Streak.CurrentDays != 1 || old.Streak.LongestDays != 1 || old.Streak.ActiveToday {
		t.Fatal(old.Streak, err)
	}
	// Leer la racha no otorga insignias.
	var badge int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM user_achievements`).Scan(&badge); err != nil || badge != 0 {
		t.Fatal("la racha de lectura no otorga insignias", badge, err)
	}
}

func TestNextCourseFromPathsThenAssignmentsOnPostgres(t *testing.T) {
	ctx, pool := database(t)
	seedPlus(t, ctx, pool)
	s := application.New(New(pool), videos{}, domain.VideoPolicy{MaxBytes: 1 << 20})
	c1 := publishPlus(t, ctx, s, "N-01", 20, nil)
	c2 := publishPlus(t, ctx, s, "N-02", 30, nil)
	c3 := publishPlus(t, ctx, s, "N-03", 40, nil)
	pass := func(c plusCourse, n int) application.AttemptOutcome {
		t.Helper()
		out, err := s.SubmitAttempt(ctx, "worker", org, c.ID, plusAttempt(c, n, "FINAL", "", []int{1, 2, 3}, 1, 1, 1))
		if err != nil || !out.Passed {
			t.Fatal(out, err)
		}
		return out
	}
	// Sin ruta ni asignaciones: null.
	if out := pass(c1, 1); out.NextCourse != nil {
		t.Fatal("sin ruta ni asignaciones", out.NextCourse)
	}
	publishPath := func(n int, title string, courses ...string) string {
		t.Helper()
		in := domain.PathInput{ID: uid(70, n), Title: title, CourseIDs: courses}
		if _, err := s.CreatePath(ctx, "admin", org, in); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PublishPath(ctx, "admin", org, in.ID); err != nil {
			t.Fatal(err)
		}
		return in.ID
	}
	// Ruta en borrador: no cuenta.
	if _, err := s.CreatePath(ctx, "admin", org, domain.PathInput{ID: uid(70, 9), Title: "Borrador", CourseIDs: []string{c1.ID, c3.ID}}); err != nil {
		t.Fatal(err)
	}
	if out := pass(c1, 2); out.NextCourse != nil {
		t.Fatal("el borrador no sugiere", out.NextCourse)
	}
	// Una ruta publicada propone el siguiente curso.
	first := publishPath(1, "Ruta uno", c1.ID, c2.ID)
	if out := pass(c1, 3); out.NextCourse == nil || *out.NextCourse != (domain.NextCourse{PathID: first, PathTitle: "Ruta uno", CourseID: c2.ID, CourseTitle: "Curso N-02"}) {
		t.Fatal("una ruta", out.NextCourse)
	}
	// Varias rutas: la publicada más recientemente.
	time.Sleep(5 * time.Millisecond)
	second := publishPath(2, "Ruta dos", c1.ID, c3.ID)
	input := plusAttempt(c1, 4, "FINAL", "", []int{1, 2, 3}, 1, 1, 1)
	out, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, input)
	if err != nil || out.NextCourse == nil || out.NextCourse.PathID != second || out.NextCourse.CourseID != c3.ID {
		t.Fatal("varias rutas", out.NextCourse, err)
	}
	if replay, err := s.SubmitAttempt(ctx, "worker", org, c1.ID, input); err != nil || !replay.Replayed || replay.NextCourse == nil || replay.NextCourse.CourseID != c3.ID {
		t.Fatal("replay", replay.NextCourse, err)
	}
	// Completar el curso 3 deja la ruta dos completa: gana la ruta uno (c2).
	if out = pass(c3, 5); out.NextCourse != nil && out.NextCourse.PathID == second {
		t.Fatal("la ruta dos ya está completa", out.NextCourse)
	}
	if out = pass(c1, 6); out.NextCourse == nil || out.NextCourse.PathID != first || out.NextCourse.CourseID != c2.ID {
		t.Fatal("ruta completa se salta", out.NextCourse)
	}
	// Con c2 completado, ambas rutas están completas: cae a las asignaciones pendientes.
	pass(c2, 7)
	if out = pass(c1, 8); out.NextCourse != nil {
		t.Fatal("rutas completas y sin asignaciones", out.NextCourse)
	}
	c4 := publishPlus(t, ctx, s, "N-04", 50, nil)
	if _, err := s.CreateAssignment(ctx, "admin", org, newAssignment(1, c4.ID, domain.AssignmentTarget{Type: domain.TargetRole, Role: "WORKER"}, nil)); err != nil {
		t.Fatal(err)
	}
	out = pass(c1, 9)
	if out.NextCourse == nil || out.NextCourse.CourseID != c4.ID || out.NextCourse.PathID != "" || out.NextCourse.PathTitle != "" {
		t.Fatal("fallback a asignaciones", out.NextCourse)
	}
}
