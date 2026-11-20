package domain

import (
	"testing"
	"time"
)

func TestCurrentStreakTable(t *testing.T) {
	lima := time.FixedZone("America/Lima", -5*3600)
	tokyo := time.FixedZone("Asia/Tokyo", 9*3600)
	at := func(loc *time.Location, y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, loc).UTC()
	}
	now := at(lima, 2026, time.November, 20, 15, 0)
	cases := []struct {
		name     string
		activity []time.Time
		now      time.Time
		loc      *time.Location
		want     Streak
	}{
		{"sin actividad", nil, now, lima, Streak{Timezone: "America/Lima"}},
		{"solo hoy", []time.Time{at(lima, 2026, 11, 20, 8, 0)}, now, lima,
			Streak{CurrentDays: 1, LongestDays: 1, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		{"termina ayer sigue vigente", []time.Time{at(lima, 2026, 11, 19, 8, 0), at(lima, 2026, 11, 18, 8, 0)}, now, lima,
			Streak{CurrentDays: 2, LongestDays: 2, LastActivityDate: "2026-11-19", Timezone: "America/Lima"}},
		{"termina anteayer se rompe", []time.Time{at(lima, 2026, 11, 18, 8, 0), at(lima, 2026, 11, 17, 8, 0), at(lima, 2026, 11, 16, 8, 0)}, now, lima,
			Streak{CurrentDays: 0, LongestDays: 3, LastActivityDate: "2026-11-18", Timezone: "America/Lima"}},
		{"varios eventos el mismo día cuentan una vez", []time.Time{at(lima, 2026, 11, 20, 1, 0), at(lima, 2026, 11, 20, 23, 0), at(lima, 2026, 11, 19, 12, 0)}, now, lima,
			Streak{CurrentDays: 2, LongestDays: 2, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		{"hueco: la vigente es la reciente y la mayor la antigua", []time.Time{
			at(lima, 2026, 11, 20, 9, 0), at(lima, 2026, 11, 19, 9, 0),
			at(lima, 2026, 11, 10, 9, 0), at(lima, 2026, 11, 9, 9, 0), at(lima, 2026, 11, 8, 9, 0), at(lima, 2026, 11, 7, 9, 0)}, now, lima,
			Streak{CurrentDays: 2, LongestDays: 4, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		{"el orden de entrada no importa", []time.Time{at(lima, 2026, 11, 18, 9, 0), at(lima, 2026, 11, 20, 9, 0), at(lima, 2026, 11, 19, 9, 0)}, now, lima,
			Streak{CurrentDays: 3, LongestDays: 3, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		// 20:00 en Lima del 19 es 01:00 UTC del 20: el día es el local, no el UTC.
		{"frontera de zona: 20:00 Lima cuenta como el día local", []time.Time{at(lima, 2026, 11, 19, 20, 0), at(lima, 2026, 11, 18, 20, 0)},
			at(lima, 2026, 11, 19, 21, 0), lima,
			Streak{CurrentDays: 2, LongestDays: 2, LastActivityDate: "2026-11-19", ActiveToday: true, Timezone: "America/Lima"}},
		{"mismo instante, otra zona, otro día", []time.Time{at(lima, 2026, 11, 19, 20, 0)}, at(lima, 2026, 11, 19, 21, 0), tokyo,
			Streak{CurrentDays: 1, LongestDays: 1, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "Asia/Tokyo"}},
		{"cruza medianoche local: 23:59 y 00:01 son días distintos", []time.Time{at(lima, 2026, 11, 19, 23, 59), at(lima, 2026, 11, 20, 0, 1)}, now, lima,
			Streak{CurrentDays: 2, LongestDays: 2, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		{"23:59 y 00:01 UTC son el mismo día en Lima", []time.Time{at(time.UTC, 2026, 11, 20, 23, 59), at(time.UTC, 2026, 11, 21, 0, 1)}, at(lima, 2026, 11, 20, 20, 0), lima,
			Streak{CurrentDays: 1, LongestDays: 1, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		{"actividad futura se ignora", []time.Time{at(lima, 2026, 11, 22, 9, 0), at(lima, 2026, 11, 20, 9, 0)}, now, lima,
			Streak{CurrentDays: 1, LongestDays: 1, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "America/Lima"}},
		{"cruce de mes y año", []time.Time{at(lima, 2027, 1, 1, 9, 0), at(lima, 2026, 12, 31, 9, 0), at(lima, 2026, 12, 30, 9, 0)}, at(lima, 2027, 1, 1, 10, 0), lima,
			Streak{CurrentDays: 3, LongestDays: 3, LastActivityDate: "2027-01-01", ActiveToday: true, Timezone: "America/Lima"}},
		{"zona nula usa UTC", []time.Time{at(time.UTC, 2026, 11, 20, 9, 0)}, at(time.UTC, 2026, 11, 20, 10, 0), nil,
			Streak{CurrentDays: 1, LongestDays: 1, LastActivityDate: "2026-11-20", ActiveToday: true, Timezone: "UTC"}},
	}
	for _, c := range cases {
		if got := CurrentStreak(c.activity, c.now, c.loc); got != c.want {
			t.Errorf("%s: obtuvo %+v, esperaba %+v", c.name, got, c.want)
		}
	}
}

func TestCurrentStreakAcrossDaylightSaving(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("sin base de zonas horarias")
	}
	// 2026-03-08 es el cambio de hora en Nueva York: ese día dura 23 horas.
	days := []time.Time{time.Date(2026, 3, 9, 7, 0, 0, 0, ny), time.Date(2026, 3, 8, 7, 0, 0, 0, ny), time.Date(2026, 3, 7, 7, 0, 0, 0, ny)}
	got := CurrentStreak(days, time.Date(2026, 3, 9, 20, 0, 0, 0, ny), ny)
	if got.CurrentDays != 3 || got.LongestDays != 3 {
		t.Fatalf("la racha debe cruzar el cambio de hora: %+v", got)
	}
}

func TestAttemptXPOnlyCountsWhatIsNew(t *testing.T) {
	passed := Grade{Score: 100, CorrectCount: 3, Total: 3, Passed: true}
	failed := Grade{Score: 33, CorrectCount: 1, Total: 3}
	fresh := Progress{Status: ProgressInProgress}
	done := Progress{Status: ProgressCompleted, ExperiencePoints: 30}
	cases := []struct {
		name           string
		kind           string
		grade          Grade
		before, after  Progress
		previousModule int
		want           int
	}{
		{"diagnóstico no da XP", AttemptDiagnostic, passed, fresh, fresh, 0, 0},
		{"FINAL aprobado da 10 por correcta", AttemptFinal, passed, fresh, fresh.AfterAttempt(AttemptFinal, passed, time.Now()), 0, 30},
		{"FINAL repetido con la misma nota da 0", AttemptFinal, passed, done, done.AfterAttempt(AttemptFinal, passed, time.Now()), 0, 0},
		{"FINAL mejor solo suma la diferencia", AttemptFinal, passed, Progress{Status: ProgressCompleted, ExperiencePoints: 20}, done, 0, 10},
		{"FINAL reprobado da 0", AttemptFinal, failed, fresh, fresh.AfterAttempt(AttemptFinal, failed, time.Now()), 0, 0},
		{"MODULE aprobado da 5 por correcta", AttemptModule, passed, fresh, fresh, 0, 15},
		{"MODULE repetido da 0", AttemptModule, passed, fresh, fresh, 15, 0},
		{"MODULE mejor suma la diferencia", AttemptModule, passed, fresh, fresh, 10, 5},
		{"MODULE peor que el anterior nunca resta", AttemptModule, passed, fresh, fresh, 20, 0},
		{"MODULE reprobado da 0", AttemptModule, failed, fresh, fresh, 0, 0},
	}
	for _, c := range cases {
		if got := AttemptXP(c.kind, c.grade, c.before, c.after, c.previousModule); got != c.want {
			t.Errorf("%s: obtuvo %d, esperaba %d", c.name, got, c.want)
		}
	}
}

func TestSelectNextCourse(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)
	course := func(id, progress string) PathCourse {
		return PathCourse{CourseID: id, Title: "Curso " + id, CourseStatus: StatusPublished, Progress: progress}
	}
	pending := func(id, status string) MyAssignment {
		return MyAssignment{Course: Course{ID: id, Title: "Curso " + id}, Status: status}
	}
	cases := []struct {
		name        string
		paths       []Path
		assignments []MyAssignment
		want        *NextCourse
	}{
		{"nada", nil, nil, nil},
		{"una ruta", []Path{{ID: "p1", Title: "Ruta 1", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("a", ProgressCompleted), course("b", ProgressNotStarted)}}}, nil,
			&NextCourse{PathID: "p1", PathTitle: "Ruta 1", CourseID: "b", CourseTitle: "Curso b"}},
		{"el siguiente pendiente salta los completados", []Path{{ID: "p1", Title: "Ruta 1", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("a", ProgressCompleted), course("b", ProgressCompleted), course("c", ProgressInProgress)}}}, nil,
			&NextCourse{PathID: "p1", PathTitle: "Ruta 1", CourseID: "c", CourseTitle: "Curso c"}},
		{"varias rutas: gana la publicada más recientemente", []Path{
			{ID: "p1", Title: "Vieja", Status: PathPublished, PublishedAt: &t1, Courses: []PathCourse{course("a", ProgressCompleted), course("b", ProgressNotStarted)}},
			{ID: "p2", Title: "Nueva", Status: PathPublished, PublishedAt: &t2, Courses: []PathCourse{course("a", ProgressCompleted), course("c", ProgressNotStarted)}},
			{ID: "p0", Title: "Antigua", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("a", ProgressCompleted), course("d", ProgressNotStarted)}},
		}, nil, &NextCourse{PathID: "p2", PathTitle: "Nueva", CourseID: "c", CourseTitle: "Curso c"}},
		{"la ruta completa se salta y gana otra", []Path{
			{ID: "p2", Title: "Completa", Status: PathPublished, PublishedAt: &t2, Courses: []PathCourse{course("a", ProgressCompleted)}},
			{ID: "p1", Title: "Pendiente", Status: PathPublished, PublishedAt: &t1, Courses: []PathCourse{course("a", ProgressCompleted), course("b", ProgressNotStarted)}},
		}, nil, &NextCourse{PathID: "p1", PathTitle: "Pendiente", CourseID: "b", CourseTitle: "Curso b"}},
		{"ruta completa cae a las asignaciones", []Path{{ID: "p1", Title: "Ruta", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("a", ProgressCompleted)}}},
			[]MyAssignment{pending("a", LearnCompleted), pending("x", LearnCompleted), pending("y", LearnOverdue), pending("z", LearnPending)},
			&NextCourse{CourseID: "y", CourseTitle: "Curso y"}},
		{"ruta completa y sin asignaciones pendientes", []Path{{ID: "p1", Title: "Ruta", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("a", ProgressCompleted)}}},
			[]MyAssignment{pending("x", LearnCompleted), pending("a", LearnPending), pending("w", LearnInProgress)}, nil},
		{"asignación pendiente sin rutas", nil, []MyAssignment{pending("z", LearnPending)}, &NextCourse{CourseID: "z", CourseTitle: "Curso z"}},
		{"ruta en borrador o sin el curso no cuenta", []Path{
			{ID: "d", Title: "Borrador", Status: PathDraft, Courses: []PathCourse{course("a", ProgressCompleted), course("b", ProgressNotStarted)}},
			{ID: "o", Title: "Otra", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("q", ProgressNotStarted)}},
		}, nil, nil},
		{"curso no publicado de la ruta no se propone", []Path{{ID: "p1", Title: "Ruta", Status: PathPublished, PublishedAt: &t0, Courses: []PathCourse{course("a", ProgressCompleted), {CourseID: "b", Title: "Curso b", CourseStatus: StatusDraft}}}}, nil, nil},
	}
	for _, c := range cases {
		got := SelectNextCourse("a", c.paths, c.assignments)
		switch {
		case got == nil && c.want == nil:
		case got == nil || c.want == nil || *got != *c.want:
			t.Errorf("%s: obtuvo %+v, esperaba %+v", c.name, got, c.want)
		}
	}
}
