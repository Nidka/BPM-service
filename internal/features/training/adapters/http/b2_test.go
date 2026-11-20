package traininghttp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
)

func attemptField(r reply, name string) (any, bool) {
	attempt, _ := r.data()["attempt"].(map[string]any)
	v, ok := attempt[name]
	return v, ok
}

func xpOf(t *testing.T, r reply) float64 {
	t.Helper()
	v, ok := attemptField(r, "xpEarned")
	if !ok {
		t.Fatalf("falta attempt.xpEarned: %s", r.Body)
	}
	return v.(float64)
}

func TestAttemptXPEarnedIncludesBadgesAndIsStableOnReplay(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	path := "/courses/" + p.course + "/attempts"
	module := func(n int, picks ...int) map[string]any {
		body := p.attempt(uid(7, n), uid(8, n), "MODULE", picks...)
		body["lessonId"] = p.lesson
		return body
	}
	// MODULE perfecto: 3 correctas x 5 + insignia PERFECT_SCORE (30).
	first := e.do("POST", path, "worker", module(1, 1, 1, 1))
	if first.Code != 201 || xpOf(t, first) != 45 {
		t.Fatalf("MODULE perfecto: %d %s", first.Code, first.Body)
	}
	if replay := e.do("POST", path, "worker", module(1, 1, 1, 1)); replay.Code != 200 || replay.Body != first.Body {
		t.Fatalf("la repetición devuelve lo mismo: %d %s", replay.Code, replay.Body)
	}
	// Repetirlo con otro id no suma nada: ni módulo ni insignias nuevas.
	if again := e.do("POST", path, "worker", module(2, 1, 1, 1)); xpOf(t, again) != 0 {
		t.Fatalf("MODULE repetido: %s", again.Body)
	}
	// DIAGNOSTIC no da XP.
	if diag := e.do("POST", path, "worker", p.attempt(uid(7, 3), uid(8, 3), "DIAGNOSTIC", 1, 1, 1)); xpOf(t, diag) != 0 {
		t.Fatalf("DIAGNOSTIC: %s", diag.Body)
	}
	// FINAL aprobado con 2 de 3: 20 de curso + FIRST_COURSE (20).
	final := e.do("POST", path, "worker", p.attempt(uid(7, 4), uid(8, 4), "FINAL", 1, 1, 0))
	if final.Code != 201 || xpOf(t, final) != 40 {
		t.Fatalf("FINAL 2/3: %d %s", final.Code, final.Body)
	}
	// FINAL perfecto suma solo la diferencia de curso (30-20); PERFECT_SCORE ya se había otorgado.
	if better := e.do("POST", path, "worker", p.attempt(uid(7, 5), uid(8, 5), "FINAL", 1, 1, 1)); xpOf(t, better) != 10 {
		t.Fatalf("FINAL mejor: %s", better.Body)
	}
	// FINAL repetido con la misma nota: 0.
	same := e.do("POST", path, "worker", p.attempt(uid(7, 6), uid(8, 6), "FINAL", 1, 1, 1))
	if same.Code != 201 || xpOf(t, same) != 0 {
		t.Fatalf("FINAL repetido: %d %s", same.Code, same.Body)
	}
	if replay := e.do("POST", path, "worker", p.attempt(uid(7, 6), uid(8, 6), "FINAL", 1, 1, 1)); replay.Code != 200 || replay.Body != same.Body {
		t.Fatalf("replay del FINAL repetido: %s", replay.Body)
	}
	// El total de XP del usuario es la suma de lo que ganó en cada intento.
	totals := e.do("GET", "/me/achievements", "worker", nil).data()["totals"].(map[string]any)
	if totals["experiencePoints"].(float64) != 45+0+0+40+10+0 {
		t.Fatalf("el total no cuadra con los intentos: %v", totals)
	}
}

func TestAttemptXPEarnedIsOmittedForHistoricAttempts(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	path := "/courses/" + p.course + "/attempts"
	body := p.attempt(uid(7, 1), uid(8, 1), "FINAL", 1, 1, 1)
	first := e.do("POST", path, "worker", body)
	if _, ok := attemptField(first, "xpEarned"); !ok {
		t.Fatalf("un intento nuevo trae xpEarned: %s", first.Body)
	}
	// V15 deja xp_awarded en NULL en los intentos anteriores.
	for i := range e.repo.attempts {
		e.repo.attempts[i].XPAwarded = nil
	}
	replay := e.do("POST", path, "worker", body)
	if replay.Code != 200 {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if _, ok := attemptField(replay, "xpEarned"); ok || strings.Contains(replay.Body, "xpEarned") {
		t.Fatalf("el intento histórico no debe traer xpEarned: %s", replay.Body)
	}
}

// publishSimple publica un curso de una lección y una pregunta (correcta: opción 1, aprueba con 67).
func (e *env) publishSimple(code, title string, n int) (course, version, question string) {
	e.t.Helper()
	created := e.do("POST", "/courses", "admin", map[string]any{"code": code, "title": title, "description": "BPM", "estimatedDurationMinutes": 10})
	if created.Code != 201 {
		e.t.Fatalf("crear curso: %d %s", created.Code, created.Body)
	}
	course = created.data()["course"].(map[string]any)["id"].(string)
	lesson, question := uid(30+n, 1), uid(40+n, 1)
	version = e.saveAndPublish(course,
		[]any{map[string]any{"id": lesson, "title": "Lección " + title, "content": "x", "displayOrder": 1}},
		[]any{map[string]any{"id": question, "lessonId": lesson, "questionText": "¿?", "options": []string{"a", "b"}, "correctOption": 1, "explanation": "x", "displayOrder": 1}})
	return course, version, question
}

func (e *env) passFinal(course, version, question string, n int) reply {
	e.t.Helper()
	body := map[string]any{"id": uid(50, n), "clientOperationId": uid(51, n), "versionId": version, "attemptType": "FINAL",
		"startedAt": "2026-05-01T10:00:00Z", "clientCreatedAt": "2026-05-01T10:05:00Z",
		"answers": []any{map[string]any{"questionId": question, "selectedOption": 1}}}
	r := e.do("POST", "/courses/"+course+"/attempts", "worker", body)
	if r.Code != 201 && r.Code != 200 {
		e.t.Fatalf("FINAL: %d %s", r.Code, r.Body)
	}
	return r
}

func (e *env) makePath(n int, title string, publish bool, courses ...string) string {
	e.t.Helper()
	id := uid(60, n)
	if r := e.do("POST", "/paths", "admin", map[string]any{"id": id, "title": title, "description": "", "courseIds": courses}); r.Code != 201 {
		e.t.Fatalf("crear ruta: %d %s", r.Code, r.Body)
	}
	if publish {
		if r := e.do("POST", "/paths/"+id+"/publish", "admin", nil); r.Code != 200 {
			e.t.Fatalf("publicar ruta: %d %s", r.Code, r.Body)
		}
		time.Sleep(2 * time.Millisecond) // las rutas se ordenan por fecha de publicación
	}
	return id
}

func nextCourseOf(t *testing.T, r reply) map[string]any {
	t.Helper()
	v, ok := r.data()["nextCourse"]
	if !ok {
		t.Fatalf("falta outcome.nextCourse (siempre presente): %s", r.Body)
	}
	if v == nil {
		return nil
	}
	return v.(map[string]any)
}

func TestNextCourseSelectionAfterPassedFinal(t *testing.T) {
	e := newEnv(t)
	a, av, aq := e.publishSimple("a-01", "Curso A", 1)
	b, _, _ := e.publishSimple("b-01", "Curso B", 2)
	c, _, _ := e.publishSimple("c-01", "Curso C", 3)

	// Sin rutas ni asignaciones: null (la clave existe), también en DIAGNOSTIC y FINAL reprobado.
	diag := map[string]any{"id": uid(50, 90), "clientOperationId": uid(51, 90), "versionId": av, "attemptType": "DIAGNOSTIC",
		"startedAt": "2026-05-01T10:00:00Z", "clientCreatedAt": "2026-05-01T10:05:00Z", "answers": []any{map[string]any{"questionId": aq, "selectedOption": 1}}}
	if r := e.do("POST", "/courses/"+a+"/attempts", "worker", diag); nextCourseOf(t, r) != nil {
		t.Fatalf("DIAGNOSTIC no sugiere: %s", r.Body)
	}
	bad := map[string]any{"id": uid(50, 91), "clientOperationId": uid(51, 91), "versionId": av, "attemptType": "FINAL",
		"startedAt": "2026-05-01T10:00:00Z", "clientCreatedAt": "2026-05-01T10:05:00Z", "answers": []any{map[string]any{"questionId": aq, "selectedOption": 0}}}
	if r := e.do("POST", "/courses/"+a+"/attempts", "worker", bad); r.Code != 201 || r.data()["attempt"].(map[string]any)["passed"] != false || nextCourseOf(t, r) != nil {
		t.Fatalf("FINAL reprobado no sugiere: %s", r.Body)
	}
	first := e.passFinal(a, av, aq, 1)
	if nextCourseOf(t, first) != nil {
		t.Fatalf("sin ruta ni asignaciones: %s", first.Body)
	}

	// Una ruta en borrador no cuenta; publicada propone su siguiente curso.
	draftPath := e.makePath(1, "Borrador", false, a, b)
	if nextCourseOf(t, e.passFinal(a, av, aq, 2)) != nil {
		t.Fatal("una ruta en borrador no debe sugerir")
	}
	_ = draftPath
	e.makePath(2, "Ruta uno", true, a, b)
	got := nextCourseOf(t, e.passFinal(a, av, aq, 3))
	if got["pathId"] != uid(60, 2) || got["pathTitle"] != "Ruta uno" || got["courseId"] != b || got["courseTitle"] != "Curso B" {
		t.Fatalf("una ruta: %v", got)
	}

	// Varias rutas: gana la publicada más recientemente.
	e.makePath(3, "Ruta dos", true, a, c)
	got = nextCourseOf(t, e.passFinal(a, av, aq, 4))
	if got["pathId"] != uid(60, 3) || got["courseId"] != c {
		t.Fatalf("varias rutas: %v", got)
	}

	// La repetición idéntica recalcula con el estado actual y conserva la forma.
	if r := e.passFinal(a, av, aq, 4); r.Code != 200 || nextCourseOf(t, r)["courseId"] != c {
		t.Fatalf("replay: %d %s", r.Code, r.Body)
	}
}

func TestNextCourseFallsBackToAssignmentsWhenPathIsComplete(t *testing.T) {
	e := newEnv(t)
	a, av, aq := e.publishSimple("a-01", "Curso A", 1)
	x, _, _ := e.publishSimple("x-01", "Curso X", 2)
	y, _, _ := e.publishSimple("y-01", "Curso Y", 3)
	e.makePath(1, "Solo A", true, a)
	worker := map[string]any{"type": "USER", "userId": "worker"}
	// Una asignación del propio curso A no puede ser la sugerencia.
	for i, course := range []string{a, x, y} {
		if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(70, i+1), course, worker, nil)); r.Code != 201 {
			t.Fatalf("asignar: %d %s", r.Code, r.Body)
		}
	}
	got := nextCourseOf(t, e.passFinal(a, av, aq, 1))
	if got == nil || got["pathId"] != nil || got["pathTitle"] != nil || got["courseId"] != x && got["courseId"] != y {
		t.Fatalf("ruta completa cae a asignaciones (sin ruta): %v", got)
	}
	if got["courseId"] == a {
		t.Fatal("no debe sugerir el curso recién aprobado")
	}
	// Con las demás asignaciones canceladas, o ya completadas, no hay nada.
	for _, i := range []int{2, 3} {
		if r := e.do("POST", fmt.Sprintf("/assignments/%s/cancel", uid(70, i)), "admin", nil); r.Code != 200 {
			t.Fatalf("cancelar: %d %s", r.Code, r.Body)
		}
	}
	if got = nextCourseOf(t, e.passFinal(a, av, aq, 2)); got != nil {
		t.Fatalf("nada pendiente: %v", got)
	}
}

func TestStreakInMyAchievements(t *testing.T) {
	e := newEnv(t)
	lima := time.FixedZone("America/Lima", -5*3600)
	day := func(daysAgo int) time.Time { return time.Now().UTC().Add(-time.Duration(daysAgo) * 24 * time.Hour) }
	streakOf := func(user string) map[string]any {
		t.Helper()
		r := e.do("GET", "/me/achievements", user, nil)
		s, ok := r.data()["streak"].(map[string]any)
		if r.Code != 200 || !ok {
			t.Fatalf("falta streak: %d %s", r.Code, r.Body)
		}
		return s
	}

	empty := streakOf("worker")
	if empty["currentDays"].(float64) != 0 || empty["longestDays"].(float64) != 0 || empty["lastActivityDate"] != nil || empty["activeToday"] != false || empty["timezone"] != "America/Lima" {
		t.Fatalf("sin actividad: %v", empty)
	}

	// Intentos hoy, ayer y anteayer y un bloque anterior de 4 días.
	for _, n := range []int{0, 1, 2, 10, 11, 12, 13} {
		e.repo.attempts = append(e.repo.attempts, application.AttemptRecord{UserID: "worker", CompletedAt: day(n)})
	}
	got := streakOf("worker")
	if got["currentDays"].(float64) != 3 || got["longestDays"].(float64) != 4 || got["activeToday"] != true || got["lastActivityDate"] != time.Now().In(lima).Format("2006-01-02") {
		t.Fatalf("racha vigente: %v", got)
	}
	// Otra persona no ve esa actividad.
	if other := streakOf("chef"); other["currentDays"].(float64) != 0 {
		t.Fatalf("la racha es por usuario: %v", other)
	}

	// Una racha que terminó ayer sigue vigente; si terminó anteayer se rompe.
	e.repo.attempts = nil
	e.repo.attempts = append(e.repo.attempts, application.AttemptRecord{UserID: "worker", CompletedAt: day(1)}, application.AttemptRecord{UserID: "worker", CompletedAt: day(2)})
	if got = streakOf("worker"); got["currentDays"].(float64) != 2 || got["activeToday"] != false {
		t.Fatalf("terminó ayer: %v", got)
	}
	e.repo.attempts = []application.AttemptRecord{{UserID: "worker", CompletedAt: day(2)}, {UserID: "worker", CompletedAt: day(3)}}
	if got = streakOf("worker"); got["currentDays"].(float64) != 0 || got["longestDays"].(float64) != 2 || got["lastActivityDate"] == nil {
		t.Fatalf("terminó anteayer: %v", got)
	}

	// Las lecciones completadas también cuentan y la zona sale de la primera sede activa.
	e.repo.attempts = nil
	c := e.publishTwoLessons()
	if r := e.do("PUT", "/courses/"+c.course+"/progress", "worker", map[string]any{"lastLessonId": c.l2, "completedLessonId": c.l1}); r.Code != 200 {
		t.Fatalf("progreso: %d %s", r.Code, r.Body)
	}
	e.repo.plus.zones[orgA+"|worker"] = "Pacific/Auckland"
	if got = streakOf("worker"); got["currentDays"].(float64) != 1 || got["activeToday"] != true || got["timezone"] != "Pacific/Auckland" {
		t.Fatalf("lección completada y zona de la sede: %v", got)
	}
	// Una zona inválida cae en America/Lima.
	e.repo.plus.zones[orgA+"|worker"] = "No/Existe"
	if got = streakOf("worker"); got["timezone"] != "America/Lima" || got["currentDays"].(float64) != 1 {
		t.Fatalf("zona inválida: %v", got)
	}
	// El catálogo de insignias sigue igual (campos previos intactos).
	if r := e.do("GET", "/me/achievements", "worker", nil); r.data()["totals"] == nil || r.data()["achievements"] == nil {
		t.Fatalf("campos previos: %s", r.Body)
	}
}
