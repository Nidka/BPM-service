package traininghttp

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// twoLessons publica un curso de dos lecciones: las preguntas 1 y 2 evalúan la primera lección y la
// 3 la segunda. La nota mínima es 67 y la respuesta correcta es siempre la opción 1.
type twoLessons struct {
	course, version string
	l1, l2          string
	questions       []string
}

func (e *env) saveAndPublish(course string, lessons []any, questions []any) string {
	e.t.Helper()
	draft := e.do("GET", "/courses/"+course+"/draft", "admin", nil)
	if draft.Code != 200 {
		e.t.Fatalf("borrador: %d %s", draft.Code, draft.Body)
	}
	version := draft.data()["version"].(map[string]any)
	saved := e.do("PUT", "/courses/"+course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 67, "rowVersion": version["rowVersion"]}, "lessons": lessons, "questions": questions})
	if saved.Code != 200 {
		e.t.Fatalf("guardar borrador: %d %s", saved.Code, saved.Body)
	}
	pub := e.do("POST", "/courses/"+course+"/publish", "admin", nil)
	if pub.Code != 200 {
		e.t.Fatalf("publicar: %d %s", pub.Code, pub.Body)
	}
	return pub.data()["version"].(map[string]any)["id"].(string)
}

func (e *env) publishTwoLessons() twoLessons {
	e.t.Helper()
	created := e.do("POST", "/courses", "admin", map[string]any{"code": "m-02", "title": "Higiene", "description": "BPM", "estimatedDurationMinutes": 20})
	if created.Code != 201 {
		e.t.Fatalf("crear curso: %d %s", created.Code, created.Body)
	}
	c := twoLessons{course: created.data()["course"].(map[string]any)["id"].(string), l1: uid(3, 1), l2: uid(3, 2)}
	lessons := []any{
		map[string]any{"id": c.l1, "title": "Lavado", "content": "Paso 1", "displayOrder": 1},
		map[string]any{"id": c.l2, "title": "Secado", "content": "Paso 2", "displayOrder": 2},
	}
	questions := []any{}
	for i := 1; i <= 3; i++ {
		id := uid(2, i)
		c.questions = append(c.questions, id)
		lesson := c.l1
		if i == 3 {
			lesson = c.l2
		}
		questions = append(questions, map[string]any{"id": id, "lessonId": lesson, "questionText": fmt.Sprintf("Pregunta %d", i), "options": []string{"a", "b", "c"}, "correctOption": 1, "explanation": "x", "displayOrder": i})
	}
	c.version = e.saveAndPublish(c.course, lessons, questions)
	return c
}

func (c twoLessons) attempt(id, kind, lesson string, picks map[string]int) map[string]any {
	answers := []any{}
	for _, q := range c.questions {
		if pick, ok := picks[q]; ok {
			answers = append(answers, map[string]any{"questionId": q, "selectedOption": pick})
		}
	}
	body := map[string]any{"id": id, "clientOperationId": strings.Replace(id, "0000000", "0000001", 1), "versionId": c.version, "attemptType": kind,
		"startedAt": "2026-05-01T10:00:00Z", "clientCreatedAt": "2026-05-01T10:05:00Z", "answers": answers}
	if lesson != "" {
		body["lessonId"] = lesson
	}
	return body
}

func progressOf(r reply) map[string]any {
	if p, ok := r.data()["progress"].(map[string]any); ok {
		return p
	}
	return nil
}

func ids(v any) []string {
	out := []string{}
	for _, item := range v.([]any) {
		out = append(out, item.(string))
	}
	return out
}

func TestLessonProgressIsIdempotentAndValidated(t *testing.T) {
	e := newEnv(t)
	c := e.publishTwoLessons()
	url := "/courses/" + c.course + "/progress"

	view := e.do("GET", "/courses/"+c.course, "worker", nil)
	p := view.data()["progress"].(map[string]any)
	if view.Code != 200 || p["totalLessons"].(float64) != 2 || p["completedLessons"].(float64) != 0 || p["percent"].(float64) != 0 || len(ids(p["completedLessonIds"])) != 0 {
		t.Fatalf("progreso inicial: %s", view.Body)
	}

	first := e.do("PUT", url, "worker", map[string]any{"lastLessonId": c.l2, "completedLessonId": c.l1})
	p = progressOf(first)
	if first.Code != 200 || p["status"] != "IN_PROGRESS" || p["lastLessonId"] != c.l2 || p["completedLessons"].(float64) != 1 || p["totalLessons"].(float64) != 2 || p["percent"].(float64) != 50 ||
		!reflect.DeepEqual(ids(p["completedLessonIds"]), []string{c.l1}) {
		t.Fatalf("completar la primera: %d %s", first.Code, first.Body)
	}
	if again := e.do("PUT", url, "worker", map[string]any{"lastLessonId": c.l2, "completedLessonId": c.l1}); again.Body != first.Body {
		t.Fatalf("completar dos veces debe ser idempotente: %s", again.Body)
	}
	// Sin completedLessonId (app anterior) el avance por lección se conserva.
	old := e.do("PUT", url, "worker", map[string]any{"lastLessonId": c.l1})
	if p = progressOf(old); old.Code != 200 || p["completedLessons"].(float64) != 1 || p["percent"].(float64) != 50 {
		t.Fatalf("PUT sin completedLessonId: %s", old.Body)
	}

	// Lecciones fuera de la versión publicada o mal formadas se rechazan sin alterar el avance.
	if r := e.do("PUT", url, "worker", map[string]any{"lastLessonId": c.l1, "completedLessonId": uid(3, 99)}); r.Code != 404 || r.errorCode() != "TRAINING_LESSON_NOT_FOUND" {
		t.Fatalf("lección ajena: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", url, "worker", map[string]any{"lastLessonId": c.l1, "completedLessonId": "x"}); r.Code != 422 || !strings.Contains(r.Body, "completedLessonId") {
		t.Fatalf("completedLessonId inválido: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", url, "worker", map[string]any{"completedLessonId": c.l2}); r.Code != 422 {
		t.Fatalf("lastLessonId sigue siendo obligatorio: %d %s", r.Code, r.Body)
	}
	after := e.do("GET", "/courses/"+c.course, "worker", nil).data()["progress"].(map[string]any)
	if after["completedLessons"].(float64) != 1 {
		t.Fatalf("un rechazo no debe marcar lecciones: %v", after)
	}

	// Otra persona no comparte el avance y el SUPER_ADMIN (sin membresía) no puede persistir.
	if chef := e.do("GET", "/courses/"+c.course, "chef", nil).data()["progress"].(map[string]any); chef["completedLessons"].(float64) != 0 {
		t.Fatalf("el avance es por persona: %v", chef)
	}
	if r := e.do("PUT", url, "root", map[string]any{"lastLessonId": c.l1, "completedLessonId": c.l1}); r.Code != 403 {
		t.Fatalf("SUPER_ADMIN no persiste: %d", r.Code)
	}
}

func TestPassedModuleCompletesLessonAndFinalGivesFullPercent(t *testing.T) {
	e := newEnv(t)
	c := e.publishTwoLessons()
	attempts := "/courses/" + c.course + "/attempts"
	// Un módulo reprobado no completa la lección.
	if r := e.do("POST", attempts, "worker", c.attempt(uid(7, 1), "MODULE", c.l1, map[string]int{c.questions[0]: 0, c.questions[1]: 0})); r.Code != 201 || progressOf(r)["completedLessons"].(float64) != 0 {
		t.Fatalf("módulo reprobado: %d %s", r.Code, r.Body)
	}
	pass := e.do("POST", attempts, "worker", c.attempt(uid(7, 2), "MODULE", c.l2, map[string]int{c.questions[2]: 1}))
	p := progressOf(pass)
	if pass.Code != 201 || p["completedLessons"].(float64) != 1 || p["percent"].(float64) != 50 || !reflect.DeepEqual(ids(p["completedLessonIds"]), []string{c.l2}) {
		t.Fatalf("módulo aprobado: %d %s", pass.Code, pass.Body)
	}
	// El replay reconstruye el mismo avance y repetir el módulo no duplica la lección.
	if replay := e.do("POST", attempts, "worker", c.attempt(uid(7, 2), "MODULE", c.l2, map[string]int{c.questions[2]: 1})); replay.Code != 200 || replay.Body != pass.Body {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if r := e.do("POST", attempts, "worker", c.attempt(uid(7, 3), "MODULE", c.l2, map[string]int{c.questions[2]: 1})); progressOf(r)["completedLessons"].(float64) != 1 {
		t.Fatalf("repetir el módulo: %s", r.Body)
	}
	// Marcar como vista una lección ya aprobada por módulo no la duplica.
	both := e.do("PUT", "/courses/"+c.course+"/progress", "worker", map[string]any{"lastLessonId": c.l2, "completedLessonId": c.l2})
	if progressOf(both)["completedLessons"].(float64) != 1 {
		t.Fatalf("VIEWED sobre MODULE_PASSED: %s", both.Body)
	}

	// Mis asignaciones traen el porcentaje; el chef (sin avance por lección) completa el curso con un FINAL.
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 1), c.course, map[string]any{"type": "ROLE", "role": "WORKER"}, nil)); r.Code != 201 {
		t.Fatalf("asignar: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 2), c.course, map[string]any{"type": "ROLE", "role": "CHEF"}, nil)); r.Code != 201 {
		t.Fatalf("asignar: %d %s", r.Code, r.Body)
	}
	percent := func(user string) float64 {
		r := e.do("GET", "/me/assignments", user, nil)
		items := r.data()["items"].([]any)
		if r.Code != 200 || len(items) != 1 {
			t.Fatalf("mis asignaciones: %d %s", r.Code, r.Body)
		}
		return items[0].(map[string]any)["percent"].(float64)
	}
	if got := percent("worker"); got != 50 {
		t.Fatalf("percent del worker = %v", got)
	}
	if got := percent("chef"); got != 0 {
		t.Fatalf("percent del chef = %v", got)
	}
	final := e.do("POST", attempts, "chef", c.attempt(uid(7, 4), "FINAL", "", map[string]int{c.questions[0]: 1, c.questions[1]: 1, c.questions[2]: 1}))
	p = progressOf(final)
	if final.Code != 201 || p["status"] != "COMPLETED" || p["percent"].(float64) != 100 || p["completedLessons"].(float64) != 2 || p["totalLessons"].(float64) != 2 {
		t.Fatalf("FINAL aprobado debe dar 100%%: %d %s", final.Code, final.Body)
	}
	if got := percent("chef"); got != 100 {
		t.Fatalf("percent del chef completado = %v", got)
	}
	get := e.do("GET", "/courses/"+c.course, "chef", nil).data()["progress"].(map[string]any)
	if get["percent"].(float64) != 100 || len(ids(get["completedLessonIds"])) != 2 {
		t.Fatalf("GET del curso completado: %v", get)
	}
}

func TestNewPublishedVersionKeepsCompletedAt100AndRejectsOldLessons(t *testing.T) {
	e := newEnv(t)
	c := e.publishTwoLessons()
	if r := e.do("POST", "/courses/"+c.course+"/attempts", "worker", c.attempt(uid(7, 1), "FINAL", "", map[string]int{c.questions[0]: 1, c.questions[1]: 1, c.questions[2]: 1})); r.Code != 201 {
		t.Fatalf("FINAL: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", "/courses/"+c.course+"/progress", "chef", map[string]any{"lastLessonId": c.l1, "completedLessonId": c.l1}); r.Code != 200 {
		t.Fatalf("avance del chef: %d %s", r.Code, r.Body)
	}
	// Se publica una versión nueva: sus lecciones tienen identificadores nuevos.
	draft := e.do("GET", "/courses/"+c.course+"/draft", "admin", nil)
	version := draft.data()["version"].(map[string]any)
	lessons := draft.data()["lessons"].([]any)
	questions := draft.data()["questions"].([]any)
	if r := e.do("PUT", "/courses/"+c.course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 67, "rowVersion": version["rowVersion"]}, "lessons": lessons, "questions": questions}); r.Code != 200 {
		t.Fatalf("guardar: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/courses/"+c.course+"/publish", "admin", nil); r.Code != 200 {
		t.Fatalf("publicar: %d %s", r.Code, r.Body)
	}
	// La lección de la versión anterior ya no pertenece a la publicada.
	if r := e.do("PUT", "/courses/"+c.course+"/progress", "chef", map[string]any{"lastLessonId": lessons[0].(map[string]any)["id"], "completedLessonId": c.l1}); r.Code != 404 || r.errorCode() != "TRAINING_LESSON_NOT_FOUND" {
		t.Fatalf("lección de otra versión: %d %s", r.Code, r.Body)
	}
	done := e.do("GET", "/courses/"+c.course, "worker", nil).data()["progress"].(map[string]any)
	if done["status"] != "COMPLETED" || done["percent"].(float64) != 100 || done["completedLessons"].(float64) != 2 {
		t.Fatalf("un curso completado conserva el 100%%: %v", done)
	}
	fresh := e.do("GET", "/courses/"+c.course, "chef", nil).data()["progress"].(map[string]any)
	if fresh["percent"].(float64) != 0 || fresh["totalLessons"].(float64) != 2 {
		t.Fatalf("el avance parcial no migra a la versión nueva: %v", fresh)
	}
}

func lessonBody(id string, order int, extra map[string]any) map[string]any {
	body := map[string]any{"id": id, "title": fmt.Sprintf("Lección %d", order), "content": "texto", "displayOrder": order}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestKeyPointsAuthoringKeepsAbsentAndClearsEmpty(t *testing.T) {
	e := newEnv(t)
	created := e.do("POST", "/courses", "admin", map[string]any{"code": "kp-1", "title": "Puntos", "description": "", "estimatedDurationMinutes": 5})
	course := created.data()["course"].(map[string]any)["id"].(string)
	l1, l2 := uid(3, 1), uid(3, 2)
	q := []any{map[string]any{"id": uid(2, 1), "lessonId": l1, "questionText": "P", "options": []string{"a", "b"}, "correctOption": 0, "explanation": "", "displayOrder": 1}}
	save := func(lessons ...any) reply {
		draft := e.do("GET", "/courses/"+course+"/draft", "admin", nil)
		version := draft.data()["version"].(map[string]any)
		return e.do("PUT", "/courses/"+course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 80, "rowVersion": version["rowVersion"]}, "lessons": lessons, "questions": q})
	}
	keyPointsOf := func(r reply, i int) []string {
		return ids(r.data()["lessons"].([]any)[i].(map[string]any)["keyPoints"])
	}

	saved := save(lessonBody(l1, 1, map[string]any{"keyPoints": []string{"  Lavar 20 segundos ", "Secar con papel"}}), lessonBody(l2, 2, nil))
	if saved.Code != 200 || !reflect.DeepEqual(keyPointsOf(saved, 0), []string{"Lavar 20 segundos", "Secar con papel"}) || len(keyPointsOf(saved, 1)) != 0 {
		t.Fatalf("guardar puntos: %d %s", saved.Code, saved.Body)
	}
	if !strings.Contains(saved.Body, `"keyPoints":[]`) {
		t.Fatalf("keyPoints siempre es un arreglo: %s", saved.Body)
	}
	// Una app anterior no envía keyPoints: se conservan los de la lección con el mismo id.
	kept := save(lessonBody(l1, 1, map[string]any{"title": "Nuevo título"}), lessonBody(l2, 2, nil))
	if kept.Code != 200 || !reflect.DeepEqual(keyPointsOf(kept, 0), []string{"Lavar 20 segundos", "Secar con papel"}) {
		t.Fatalf("ausente conserva: %d %s", kept.Code, kept.Body)
	}
	nullKept := save(lessonBody(l1, 1, map[string]any{"keyPoints": nil}), lessonBody(l2, 2, nil))
	if len(keyPointsOf(nullKept, 0)) != 2 {
		t.Fatalf("null equivale a ausente: %s", nullKept.Body)
	}
	if got := e.do("GET", "/courses/"+course+"/draft", "admin", nil); !reflect.DeepEqual(keyPointsOf(got, 0), []string{"Lavar 20 segundos", "Secar con papel"}) {
		t.Fatalf("GET del borrador: %s", got.Body)
	}
	// Un arreglo vacío explícito los borra.
	cleared := save(lessonBody(l1, 1, map[string]any{"keyPoints": []string{}}), lessonBody(l2, 2, nil))
	if cleared.Code != 200 || len(keyPointsOf(cleared, 0)) != 0 {
		t.Fatalf("[] vacía: %d %s", cleared.Code, cleared.Body)
	}

	// Validación: se reportan por lección y no se guarda nada.
	long := strings.Repeat("x", 201)
	for name, points := range map[string][]string{
		"más de 5":       {"a", "b", "c", "d", "e", "f"},
		"vacío":          {"ok", "   "},
		"muy largo":      {long},
		"duplicado":      {"Lavar", " lavar "},
		"salto de línea": {"uno\ndos"},
	} {
		r := save(lessonBody(l1, 1, map[string]any{"keyPoints": points}), lessonBody(l2, 2, nil))
		if r.Code != 422 || !strings.Contains(r.Body, "lessons[0].keyPoints") {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body)
		}
	}
	if ok := save(lessonBody(l1, 1, map[string]any{"keyPoints": []string{strings.Repeat("y", 200), "b", "c", "d", "e"}}), lessonBody(l2, 2, nil)); ok.Code != 200 {
		t.Fatalf("5 puntos de 200 caracteres es válido: %d %s", ok.Code, ok.Body)
	}
}

func TestKeyPointsReachLearnerOfflineAndNextDraft(t *testing.T) {
	e := newEnv(t)
	created := e.do("POST", "/courses", "admin", map[string]any{"code": "kp-2", "title": "Puntos", "description": "", "estimatedDurationMinutes": 5})
	course := created.data()["course"].(map[string]any)["id"].(string)
	l1, l2 := uid(3, 1), uid(3, 2)
	lessons := []any{lessonBody(l1, 1, map[string]any{"keyPoints": []string{"Lavar", "Secar"}}), lessonBody(l2, 2, nil)}
	questions := []any{map[string]any{"id": uid(2, 1), "lessonId": l1, "questionText": "P", "options": []string{"a", "b"}, "correctOption": 0, "explanation": "", "displayOrder": 1}}
	e.saveAndPublish(course, lessons, questions)

	view := e.do("GET", "/courses/"+course, "worker", nil)
	got := view.data()["lessons"].([]any)
	if view.Code != 200 || !reflect.DeepEqual(ids(got[0].(map[string]any)["keyPoints"]), []string{"Lavar", "Secar"}) || !strings.Contains(string(view.Body), `"keyPoints":[]`) {
		t.Fatalf("lección de alumno: %s", view.Body)
	}
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 1), course, map[string]any{"type": "ROLE", "role": "WORKER"}, nil)); r.Code != 201 {
		t.Fatalf("asignar: %d %s", r.Code, r.Body)
	}
	offline := e.at("GET", "/organizations/"+orgA+"/training/me/offline-catalog", "worker")
	body := offline.Body.String()
	if offline.Code != 200 || !strings.Contains(body, `"keyPoints":["Lavar","Secar"]`) || !strings.Contains(body, `"keyPoints":[]`) {
		t.Fatalf("catálogo offline: %d %s", offline.Code, body)
	}
	for _, leak := range []string{"completedLessonIds", "percent", "totalLessons"} {
		if strings.Contains(body, leak) {
			t.Fatalf("el catálogo offline no debe traer datos del usuario: %q", leak)
		}
	}
	// El siguiente borrador copia los puntos de la versión publicada.
	next := e.do("GET", "/courses/"+course+"/draft", "admin", nil)
	if !reflect.DeepEqual(ids(next.data()["lessons"].([]any)[0].(map[string]any)["keyPoints"]), []string{"Lavar", "Secar"}) {
		t.Fatalf("nuevo borrador: %s", next.Body)
	}
}
