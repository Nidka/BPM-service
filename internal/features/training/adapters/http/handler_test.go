package traininghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/go-chi/chi/v5"
)

const (
	orgA = "orgA"
	orgB = "orgB"
)

type tokenVerifier struct{}

func (tokenVerifier) VerifyIDToken(_ context.Context, raw string) (contracts.Identity, error) {
	return contracts.Identity{UID: raw, EmailVerified: true}, nil
}

type env struct {
	t      *testing.T
	repo   *fakeRepo
	videos *fakeVideos
	logs   *bytes.Buffer
	router http.Handler
}

func uid(kind, n int) string { return fmt.Sprintf("%08d-0000-4000-8000-%012d", kind, n) }

func newEnv(t *testing.T) *env {
	t.Helper()
	repo := newFakeRepo()
	for key, role := range map[string]string{orgA + "|admin": "ADMIN", orgA + "|chef": "CHEF", orgA + "|worker": "WORKER", orgB + "|badmin": "ADMIN"} {
		repo.roles[key] = role
	}
	repo.supers["root"] = true
	videos := &fakeVideos{}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	service := application.New(repo, videos, domain.VideoPolicy{MaxBytes: 1 << 20})
	router := chi.NewRouter()
	New(service, logger).Register(router, middleware.Authenticate(tokenVerifier{}))
	return &env{t: t, repo: repo, videos: videos, logs: logs, router: router}
}

type reply struct {
	Code int
	Body string
	JSON map[string]any
}

func (e *env) do(method, path, token string, body any) reply {
	e.t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else if raw, ok := body.(string); ok {
		reader = bytes.NewReader([]byte(raw))
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, "/organizations/"+orgA+"/training"+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	out := reply{Code: rec.Code, Body: rec.Body.String()}
	_ = json.Unmarshal(rec.Body.Bytes(), &out.JSON)
	return out
}

func (r reply) errorCode() string {
	errs, _ := r.JSON["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	first, _ := errs[0].(map[string]any)
	code, _ := first["code"].(string)
	return code
}

func (r reply) data() map[string]any {
	data, _ := r.JSON["data"].(map[string]any)
	return data
}

type published struct {
	course, version, lesson, video string
	questions                      []string
}

// publish crea un curso por la API y lo publica con 3 preguntas cuya respuesta correcta es 1.
func (e *env) publish(withVideo bool) published {
	e.t.Helper()
	created := e.do("POST", "/courses", "admin", map[string]any{"code": "m-01", "title": "Manos limpias", "description": "BPM", "estimatedDurationMinutes": 15})
	if created.Code != 201 {
		e.t.Fatalf("crear curso: %d %s", created.Code, created.Body)
	}
	p := published{course: created.data()["course"].(map[string]any)["id"].(string), lesson: uid(3, 1)}
	draft := e.do("GET", "/courses/"+p.course+"/draft", "admin", nil)
	if draft.Code != 200 {
		e.t.Fatalf("borrador: %d %s", draft.Code, draft.Body)
	}
	lesson := map[string]any{"id": p.lesson, "title": "Lavado", "content": "Paso 1\nPaso 2", "displayOrder": 1, "videoMediaId": nil}
	if withVideo {
		p.video = uid(5, 1)
		e.repo.media[p.video] = domain.VideoFile{ID: p.video, OrganizationID: orgA, MIMEType: "video/mp4", SizeBytes: 99, Status: "AVAILABLE", ObjectKey: "organizations/" + orgA + "/training/videos/" + p.video + ".mp4"}
		lesson["videoMediaId"] = p.video
	}
	questions := []any{}
	for i := 1; i <= 3; i++ {
		id := uid(2, i)
		p.questions = append(p.questions, id)
		questions = append(questions, map[string]any{"id": id, "lessonId": p.lesson, "questionText": fmt.Sprintf("Pregunta %d", i), "options": []string{"a", "b", "c"}, "correctOption": 1, "explanation": "porque sí", "displayOrder": i})
	}
	version := draft.data()["version"].(map[string]any)
	saved := e.do("PUT", "/courses/"+p.course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 67, "rowVersion": version["rowVersion"]}, "lessons": []any{lesson}, "questions": questions})
	if saved.Code != 200 {
		e.t.Fatalf("guardar borrador: %d %s", saved.Code, saved.Body)
	}
	pub := e.do("POST", "/courses/"+p.course+"/publish", "admin", nil)
	if pub.Code != 200 {
		e.t.Fatalf("publicar: %d %s", pub.Code, pub.Body)
	}
	p.version = pub.data()["version"].(map[string]any)["id"].(string)
	return p
}

func (p published) attempt(id, op, kind string, picks ...int) map[string]any {
	answers := []any{}
	for i, pick := range picks {
		answers = append(answers, map[string]any{"questionId": p.questions[i], "selectedOption": pick})
	}
	return map[string]any{"id": id, "clientOperationId": op, "versionId": p.version, "attemptType": kind,
		"startedAt": "2026-05-01T10:00:00Z", "clientCreatedAt": "2026-05-01T10:05:00Z", "answers": answers}
}

func TestAuthenticationAndStrictInput(t *testing.T) {
	e := newEnv(t)
	if r := e.do("GET", "/courses", "", nil); r.Code != 401 {
		t.Fatalf("sin token debe ser 401: %d", r.Code)
	}
	for name, body := range map[string]any{
		"campo extra":     `{"code":"A1","title":"Curso","organizationId":"orgB"}`,
		"duplicado":       `{"code":"A1","code":"A2","title":"Curso"}`,
		"json inválido":   `{`,
		"documento doble": `{"code":"A1","title":"Curso"}{}`,
	} {
		if r := e.do("POST", "/courses", "admin", body); r.Code != 400 || r.errorCode() != "INVALID_REQUEST" {
			t.Fatalf("%s debe ser 400: %d %s", name, r.Code, r.Body)
		}
	}
	if r := e.do("GET", "/courses?status=BOGUS", "admin", nil); r.Code != 422 {
		t.Fatalf("estado inválido debe ser 422: %d", r.Code)
	}
	if r := e.do("GET", "/courses?other=1", "admin", nil); r.Code != 400 {
		t.Fatalf("query desconocida debe ser 400: %d", r.Code)
	}
	if r := e.do("POST", "/courses/"+uid(1, 1)+"/publish", "admin", `{"x":1}`); r.Code != 400 {
		t.Fatalf("publish no admite cuerpo: %d", r.Code)
	}
}

func TestRolePermissionsForAuthoringAndLearning(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	for _, token := range []string{"chef", "worker"} {
		for _, call := range []struct{ method, path string }{
			{"GET", "/courses"}, {"POST", "/courses"}, {"GET", "/courses/" + p.course + "/draft"}, {"PUT", "/courses/" + p.course + "/draft"},
			{"POST", "/courses/" + p.course + "/publish"}, {"POST", "/videos/uploads"}, {"POST", "/videos/" + uid(5, 1) + "/confirm"},
		} {
			var body any
			if call.method != "GET" && !strings.HasSuffix(call.path, "publish") && !strings.HasSuffix(call.path, "confirm") {
				body = map[string]any{}
			}
			if r := e.do(call.method, call.path, token, body); r.Code != 403 || r.errorCode() != "ACCESS_DENIED" {
				t.Fatalf("%s %s %s debe ser 403: %d %s", token, call.method, call.path, r.Code, r.Body)
			}
		}
		if r := e.do("GET", "/courses/"+p.course, token, nil); r.Code != 200 {
			t.Fatalf("%s debe poder aprender: %d %s", token, r.Code, r.Body)
		}
	}
	// ADMIN autor y aprendiz; SUPER_ADMIN lee y estudia, pero sin membresía no persiste progreso ni intentos.
	if r := e.do("GET", "/courses", "admin", nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	if r := e.do("GET", "/courses/"+p.course, "root", nil); r.Code != 200 || r.data()["progress"].(map[string]any)["status"] != "NOT_STARTED" {
		t.Fatalf("SUPER_ADMIN debe leer: %d %s", r.Code, r.Body)
	}
	if r := e.do("GET", "/courses", "root", nil); r.Code != 200 {
		t.Fatalf("SUPER_ADMIN administra: %d", r.Code)
	}
	if r := e.do("PUT", "/courses/"+p.course+"/progress", "root", map[string]any{"lastLessonId": p.lesson}); r.Code != 403 {
		t.Fatalf("SUPER_ADMIN no persiste progreso: %d", r.Code)
	}
	if r := e.do("POST", "/courses/"+p.course+"/attempts", "root", p.attempt(uid(7, 1), uid(8, 1), "FINAL", 1, 1, 1)); r.Code != 403 {
		t.Fatalf("SUPER_ADMIN no crea intentos: %d", r.Code)
	}
	if r := e.do("POST", "/courses/"+p.course+"/study/answers", "root", map[string]any{"questionId": p.questions[0], "selectedOption": 1}); r.Code != 200 {
		t.Fatalf("SUPER_ADMIN puede estudiar: %d", r.Code)
	}
	// Quien no pertenece a la organización recibe 403 sin saber si el curso existe.
	if r := e.do("GET", "/courses/"+p.course, "badmin", nil); r.Code != 403 {
		t.Fatalf("no miembro: %d", r.Code)
	}
	if r := e.do("GET", "/courses/"+p.course, "stranger", nil); r.Code != 403 {
		t.Fatalf("sin membresía: %d", r.Code)
	}
}

func TestForeignTenantResourcesAreNotFound(t *testing.T) {
	e := newEnv(t)
	// Curso publicado en la organización B por su propio administrador.
	foreign := &fakeCourse{org: orgB, c: domain.Course{ID: uid(1, 9), Code: "X", Title: "Ajeno", Status: domain.StatusPublished}}
	foreign.versions = []*fakeVersion{{status: domain.StatusPublished, v: domain.Version{ID: uid(4, 9), Number: 1, PassingScore: 50, RowVersion: 1},
		lessons:   []domain.Lesson{{ID: uid(3, 9), Title: "L", DisplayOrder: 1, VideoMediaID: uid(5, 9)}},
		questions: []domain.Question{{ID: uid(2, 9), Text: "Q", Options: []string{"a", "b"}, CorrectOption: 0, DisplayOrder: 1}}}}
	e.repo.courses[foreign.c.ID] = foreign
	e.repo.media[uid(5, 9)] = domain.VideoFile{ID: uid(5, 9), OrganizationID: orgB, Status: "AVAILABLE", MIMEType: "video/mp4", SizeBytes: 5}
	id := foreign.c.ID
	attempt := map[string]any{"id": uid(7, 9), "clientOperationId": uid(8, 9), "versionId": uid(4, 9), "attemptType": "FINAL", "startedAt": "2026-05-01T10:00:00Z", "clientCreatedAt": "2026-05-01T10:01:00Z", "answers": []any{map[string]any{"questionId": uid(2, 9), "selectedOption": 0}}}
	for _, call := range []struct {
		method, path, token string
		body                any
	}{
		{"GET", "/courses/" + id, "admin", nil},
		{"GET", "/courses/" + id, "worker", nil},
		{"POST", "/courses/" + id + "/lessons/" + uid(3, 9) + "/video", "chef", nil},
		{"PUT", "/courses/" + id + "/progress", "chef", map[string]any{"lastLessonId": uid(3, 9)}},
		{"POST", "/courses/" + id + "/study/answers", "chef", map[string]any{"questionId": uid(2, 9), "selectedOption": 0}},
		{"POST", "/courses/" + id + "/attempts", "chef", attempt},
		{"GET", "/courses/" + id + "/draft", "admin", nil},
		{"PUT", "/courses/" + id + "/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 80, "rowVersion": "1"}, "lessons": []any{}, "questions": []any{}}},
		{"POST", "/courses/" + id + "/publish", "admin", nil},
		{"POST", "/videos/" + uid(5, 9) + "/confirm", "admin", nil},
	} {
		r := e.do(call.method, call.path, call.token, call.body)
		if r.Code != 404 {
			t.Fatalf("%s %s debe ser 404 por tenant ajeno: %d %s", call.method, call.path, r.Code, r.Body)
		}
		if strings.Contains(r.Body, "Ajeno") || strings.Contains(r.Body, "orgB") {
			t.Fatal("la respuesta revela datos del otro tenant")
		}
	}
	// El video ajeno tampoco aparece en el listado ni puede referenciarse en un borrador propio.
	if r := e.do("GET", "/courses", "admin", nil); strings.Contains(r.Body, "Ajeno") {
		t.Fatal("el listado revela cursos ajenos")
	}
	own := e.publish(false)
	draft := e.do("GET", "/courses/"+own.course+"/draft", "admin", nil)
	version := draft.data()["version"].(map[string]any)
	r := e.do("PUT", "/courses/"+own.course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 80, "rowVersion": version["rowVersion"]},
		"lessons": []any{map[string]any{"id": uid(3, 7), "title": "L", "content": "", "displayOrder": 1, "videoMediaId": uid(5, 9)}}, "questions": []any{}})
	if r.Code != 422 {
		t.Fatalf("referenciar un video ajeno debe ser 422: %d %s", r.Code, r.Body)
	}
}

func TestLearningViewNeverExposesAnswers(t *testing.T) {
	e := newEnv(t)
	p := e.publish(true)
	r := e.do("GET", "/courses/"+p.course, "worker", nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	lower := strings.ToLower(r.Body)
	for _, forbidden := range []string{"correctoption", "explanation", "porque sí", "signature", "organizations/", "objectkey"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("la vista de aprendizaje filtra %q: %s", forbidden, r.Body)
		}
	}
	data := r.data()
	questions := data["questions"].([]any)
	if len(questions) != 3 || questions[0].(map[string]any)["options"] == nil {
		t.Fatalf("faltan preguntas: %s", r.Body)
	}
	lesson := data["lessons"].([]any)[0].(map[string]any)
	video := lesson["video"].(map[string]any)
	if video["mediaId"] != p.video || video["mimeType"] != "video/mp4" || video["sizeBytes"].(float64) != 99 {
		t.Fatalf("video mal proyectado: %v", video)
	}
	if data["version"].(map[string]any)["passingScore"].(float64) != 67 || data["certificate"] != nil {
		t.Fatalf("versión o constancia incorrectas: %s", r.Body)
	}
	// Un video PENDING no se expone.
	pending := e.repo.media[p.video]
	pending.Status = "PENDING_UPLOAD"
	e.repo.media[p.video] = pending
	r = e.do("GET", "/courses/"+p.course, "worker", nil)
	if r.data()["lessons"].([]any)[0].(map[string]any)["video"] != nil {
		t.Fatal("un video no AVAILABLE debe ser null")
	}
	if r := e.do("POST", "/courses/"+p.course+"/lessons/"+p.lesson+"/video", "worker", nil); r.Code != 404 {
		t.Fatalf("sin video AVAILABLE debe ser 404: %d", r.Code)
	}
	// Un curso sin publicar no es visible para aprender.
	draftOnly := e.do("POST", "/courses", "admin", map[string]any{"code": "B-02", "title": "Borrador"})
	if r := e.do("GET", "/courses/"+draftOnly.data()["course"].(map[string]any)["id"].(string), "worker", nil); r.Code != 404 || r.errorCode() != "TRAINING_COURSE_NOT_FOUND" {
		t.Fatalf("curso sin publicar: %d %s", r.Code, r.Body)
	}
}

func TestStudyModeGivesFeedbackWithoutPersistence(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	attemptsBefore := len(e.repo.attempts)
	progressBefore := len(e.repo.progress)
	right := e.do("POST", "/courses/"+p.course+"/study/answers", "worker", map[string]any{"questionId": p.questions[0], "selectedOption": 1})
	if right.Code != 200 || right.data()["correct"] != true || right.data()["correctOption"].(float64) != 1 || right.data()["explanation"] != "porque sí" {
		t.Fatalf("%d %s", right.Code, right.Body)
	}
	wrong := e.do("POST", "/courses/"+p.course+"/study/answers", "worker", map[string]any{"questionId": p.questions[0], "selectedOption": 0})
	if wrong.data()["correct"] != false || wrong.data()["correctOption"].(float64) != 1 {
		t.Fatalf("%s", wrong.Body)
	}
	for name, body := range map[string]any{
		"fuera de rango": map[string]any{"questionId": p.questions[0], "selectedOption": 3},
		"negativa":       map[string]any{"questionId": p.questions[0], "selectedOption": -1},
		"sin opción":     map[string]any{"questionId": p.questions[0]},
	} {
		if r := e.do("POST", "/courses/"+p.course+"/study/answers", "worker", body); r.Code != 422 || r.errorCode() != "VALIDATION_ERROR" {
			t.Fatalf("%s debe ser 422: %d %s", name, r.Code, r.Body)
		}
	}
	if r := e.do("POST", "/courses/"+p.course+"/study/answers", "worker", map[string]any{"questionId": uid(2, 99), "selectedOption": 0}); r.Code != 404 {
		t.Fatalf("pregunta ajena a la versión: %d", r.Code)
	}
	if len(e.repo.attempts) != attemptsBefore || len(e.repo.progress) != progressBefore || len(e.repo.certs) != 0 {
		t.Fatal("el modo estudio no debe crear intentos, progreso ni constancias")
	}
}

func TestProgressTracksLastLessonAndNeverRegressesCompleted(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	r := e.do("PUT", "/courses/"+p.course+"/progress", "worker", map[string]any{"lastLessonId": p.lesson})
	progress := r.data()["progress"].(map[string]any)
	if r.Code != 200 || progress["status"] != "IN_PROGRESS" || progress["lastLessonId"] != p.lesson {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	again := e.do("PUT", "/courses/"+p.course+"/progress", "worker", map[string]any{"lastLessonId": p.lesson})
	if again.Body != r.Body {
		t.Fatal("el progreso debe ser idempotente")
	}
	if r := e.do("PUT", "/courses/"+p.course+"/progress", "worker", map[string]any{"lastLessonId": uid(3, 77)}); r.Code != 404 || r.errorCode() != "TRAINING_LESSON_NOT_FOUND" {
		t.Fatalf("lección ajena: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", "/courses/"+p.course+"/progress", "worker", map[string]any{"lastLessonId": "x"}); r.Code != 422 {
		t.Fatalf("lección inválida: %d", r.Code)
	}
	if r := e.do("POST", "/courses/"+p.course+"/attempts", "worker", p.attempt(uid(7, 1), uid(8, 1), "FINAL", 1, 1, 1)); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	done := e.do("PUT", "/courses/"+p.course+"/progress", "worker", map[string]any{"lastLessonId": p.lesson})
	if done.data()["progress"].(map[string]any)["status"] != "COMPLETED" {
		t.Fatalf("COMPLETED no debe retroceder: %s", done.Body)
	}
}

func TestAttemptsAreGradedByServerWithIdempotency(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	path := "/courses/" + p.course + "/attempts"

	// DIAGNOSTIC: califica pero no completa ni otorga XP ni constancia.
	diag := e.do("POST", path, "worker", p.attempt(uid(7, 1), uid(8, 1), "DIAGNOSTIC", 1, 1, 1))
	if diag.Code != 201 || diag.data()["attempt"].(map[string]any)["score"].(float64) != 100 || diag.data()["certificate"] != nil {
		t.Fatalf("diagnóstico: %d %s", diag.Code, diag.Body)
	}
	progress := diag.data()["progress"].(map[string]any)
	if progress["status"] != "IN_PROGRESS" || progress["experiencePoints"].(float64) != 0 || progress["bestScore"] != nil {
		t.Fatalf("el diagnóstico solo inicia el curso: %v", progress)
	}

	// FINAL aprobado en el límite exacto: 2/3 = 67 con mínimo 67.
	final := p.attempt(uid(7, 2), uid(8, 2), "FINAL", 1, 1, 0)
	first := e.do("POST", path, "worker", final)
	attempt := first.data()["attempt"].(map[string]any)
	if first.Code != 201 || attempt["score"].(float64) != 67 || attempt["passed"] != true {
		t.Fatalf("límite de aprobación: %d %s", first.Code, first.Body)
	}
	cert := first.data()["certificate"].(map[string]any)
	if len(cert["verificationCode"].(string)) < 16 {
		t.Fatalf("código débil: %v", cert)
	}
	results := first.data()["results"].([]any)
	if len(results) != 3 || results[2].(map[string]any)["correct"] != false || results[2].(map[string]any)["correctOption"].(float64) != 1 || results[0].(map[string]any)["explanation"] != "porque sí" {
		t.Fatalf("resultados: %s", first.Body)
	}
	progress = first.data()["progress"].(map[string]any)
	if progress["status"] != "COMPLETED" || progress["bestScore"].(float64) != 67 || progress["experiencePoints"].(float64) != 20 {
		t.Fatalf("progreso tras aprobar: %v", progress)
	}

	// Reintento idéntico: 200 y el mismo cuerpo, sin duplicar intentos ni constancias.
	replay := e.do("POST", path, "worker", final)
	if replay.Code != 200 || replay.Body != first.Body {
		t.Fatalf("replay: %d\n%s\n%s", replay.Code, replay.Body, first.Body)
	}
	if len(e.repo.attempts) != 2 || len(e.repo.certs) != 1 {
		t.Fatalf("el replay duplicó datos: %d intentos, %d constancias", len(e.repo.attempts), len(e.repo.certs))
	}

	// Mismo clientOperationId o id con contenido distinto: conflicto de idempotencia.
	changed := p.attempt(uid(7, 2), uid(8, 2), "FINAL", 1, 0, 0)
	if r := e.do("POST", path, "worker", changed); r.Code != 409 || r.errorCode() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("contenido distinto: %d %s", r.Code, r.Body)
	}
	otherID := p.attempt(uid(7, 3), uid(8, 2), "FINAL", 1, 1, 0)
	if r := e.do("POST", path, "worker", otherID); r.Code != 409 || r.errorCode() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("misma operación con otro id: %d %s", r.Code, r.Body)
	}
	// Otro usuario no puede reutilizar el identificador de este intento.
	if r := e.do("POST", path, "chef", final); r.Code != 409 || r.errorCode() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("otro usuario con el mismo id: %d %s", r.Code, r.Body)
	}

	// Un FINAL posterior peor no retrocede nota, XP ni estado, y la constancia es única.
	worse := e.do("POST", path, "worker", p.attempt(uid(7, 4), uid(8, 4), "FINAL", 0, 0, 0))
	progress = worse.data()["progress"].(map[string]any)
	if worse.Code != 201 || worse.data()["attempt"].(map[string]any)["passed"] != false || progress["status"] != "COMPLETED" || progress["bestScore"].(float64) != 67 || progress["experiencePoints"].(float64) != 20 {
		t.Fatalf("intento posterior: %s", worse.Body)
	}
	if len(e.repo.certs) != 1 {
		t.Fatal("la constancia debe ser única por usuario y versión")
	}
}

func TestAttemptRejectsInvalidAnswersAndOutdatedVersion(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	path := "/courses/" + p.course + "/attempts"
	cases := map[string]map[string]any{
		"faltante":    p.attempt(uid(7, 1), uid(8, 1), "FINAL", 1, 1),
		"vacío":       p.attempt(uid(7, 2), uid(8, 2), "FINAL"),
		"fuera rango": p.attempt(uid(7, 3), uid(8, 3), "FINAL", 1, 1, 9),
	}
	dup := p.attempt(uid(7, 4), uid(8, 4), "FINAL", 1, 1, 1)
	dup["answers"] = []any{map[string]any{"questionId": p.questions[0], "selectedOption": 1}, map[string]any{"questionId": p.questions[0], "selectedOption": 1}, map[string]any{"questionId": p.questions[1], "selectedOption": 1}}
	cases["duplicada"] = dup
	foreign := p.attempt(uid(7, 5), uid(8, 5), "FINAL", 1, 1, 1)
	foreign["answers"].([]any)[2].(map[string]any)["questionId"] = uid(2, 99)
	cases["pregunta ajena"] = foreign
	for name, body := range cases {
		r := e.do("POST", path, "worker", body)
		if r.Code != 422 || r.errorCode() != "TRAINING_ANSWERS_INVALID" {
			t.Fatalf("%s debe ser 422 TRAINING_ANSWERS_INVALID: %d %s", name, r.Code, r.Body)
		}
		if fields, _ := r.JSON["errors"].([]any)[0].(map[string]any)["fields"].(map[string]any); len(fields) == 0 {
			t.Fatalf("%s: falta el detalle en fields", name)
		}
	}
	noOption := `{"id":"` + uid(7, 6) + `","clientOperationId":"` + uid(8, 6) + `","versionId":"` + p.version + `","attemptType":"FINAL","startedAt":"2026-05-01T10:00:00Z","clientCreatedAt":"2026-05-01T10:05:00Z","answers":[{"questionId":"` + p.questions[0] + `"}]}`
	if r := e.do("POST", path, "worker", noOption); r.Code != 422 || r.errorCode() != "TRAINING_ANSWERS_INVALID" {
		t.Fatalf("respuesta sin opción: %d %s", r.Code, r.Body)
	}
	// El cliente no controla puntaje, aprobación ni XP: los campos extra se rechazan.
	forged := p.attempt(uid(7, 7), uid(8, 7), "FINAL", 0, 0, 0)
	forged["score"], forged["passed"], forged["experiencePoints"] = 100, true, 999
	if r := e.do("POST", path, "worker", forged); r.Code != 400 {
		t.Fatalf("campos de puntaje del cliente deben rechazarse: %d", r.Code)
	}
	for name, mutate := range map[string]func(map[string]any){
		"tipo":      func(b map[string]any) { b["attemptType"] = "MODULE" },
		"versionId": func(b map[string]any) { b["versionId"] = "x" },
	} {
		body := p.attempt(uid(7, 8), uid(8, 8), "FINAL", 1, 1, 1)
		mutate(body)
		if r := e.do("POST", path, "worker", body); r.Code != 422 {
			t.Fatalf("%s inválido debe ser 422: %d %s", name, r.Code, r.Body)
		}
	}
	if len(e.repo.attempts) != 0 {
		t.Fatal("ningún intento inválido debe persistirse")
	}

	// Nueva publicación: la versión anterior queda desactualizada (409), pero un replay anterior se conserva.
	good := p.attempt(uid(7, 9), uid(8, 9), "DIAGNOSTIC", 1, 1, 1)
	if r := e.do("POST", path, "worker", good); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	draft := e.do("GET", "/courses/"+p.course+"/draft", "admin", nil)
	version := draft.data()["version"].(map[string]any)
	if r := e.do("PUT", "/courses/"+p.course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 90, "rowVersion": version["rowVersion"]},
		"lessons":   draft.data()["lessons"],
		"questions": draft.data()["questions"]}); r.Code != 200 {
		t.Fatalf("editar copia: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/courses/"+p.course+"/publish", "admin", nil); r.Code != 200 {
		t.Fatalf("publicar v2: %d %s", r.Code, r.Body)
	}
	stale := p.attempt(uid(7, 10), uid(8, 10), "FINAL", 1, 1, 1)
	if r := e.do("POST", path, "worker", stale); r.Code != 409 || r.errorCode() != "TRAINING_VERSION_OUTDATED" {
		t.Fatalf("versión antigua: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", path, "worker", good); r.Code != 200 {
		t.Fatalf("un reintento tardío debe devolver el intento original: %d %s", r.Code, r.Body)
	}
}

func TestDraftLifecycleConcurrencyAndPublication(t *testing.T) {
	e := newEnv(t)
	created := e.do("POST", "/courses", "admin", map[string]any{"code": " c-9 ", "title": "Curso"})
	course := created.data()["course"].(map[string]any)
	if created.Code != 201 || course["code"] != "C-9" || course["status"] != "DRAFT" {
		t.Fatalf("%d %s", created.Code, created.Body)
	}
	if r := e.do("POST", "/courses", "admin", map[string]any{"code": "C-9", "title": "Otro"}); r.Code != 409 || r.errorCode() != "TRAINING_COURSE_CODE_TAKEN" {
		t.Fatalf("código repetido: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/courses", "admin", map[string]any{"code": "!!", "title": "x"}); r.Code != 422 {
		t.Fatalf("datos inválidos: %d", r.Code)
	}
	id := course["id"].(string)
	draft := e.do("GET", "/courses/"+id+"/draft", "admin", nil)
	version := draft.data()["version"].(map[string]any)
	if draft.Code != 200 || version["rowVersion"] != "1" || version["passingScore"].(float64) != 80 || len(draft.data()["lessons"].([]any)) != 0 {
		t.Fatalf("borrador vacío: %s", draft.Body)
	}
	// Publicar un borrador vacío o con video pendiente es incompleto.
	if r := e.do("POST", "/courses/"+id+"/publish", "admin", nil); r.Code != 422 || r.errorCode() != "TRAINING_DRAFT_INCOMPLETE" {
		t.Fatalf("borrador vacío: %d %s", r.Code, r.Body)
	}
	lesson := func(video any) map[string]any {
		return map[string]any{"id": uid(3, 1), "title": "L1", "content": "x", "displayOrder": 1, "videoMediaId": video}
	}
	question := func() map[string]any {
		return map[string]any{"id": uid(2, 1), "lessonId": uid(3, 1), "questionText": "¿Q?", "options": []string{"a", "b"}, "correctOption": 0, "explanation": "", "displayOrder": 1}
	}
	body := func(row any, video any) map[string]any {
		return map[string]any{"version": map[string]any{"passingScore": 80, "rowVersion": row}, "lessons": []any{lesson(video)}, "questions": []any{question()}}
	}
	// Validaciones por campo.
	bad := body("1", nil)
	bad["questions"].([]any)[0].(map[string]any)["options"] = []string{"solo"}
	bad["lessons"].([]any)[0].(map[string]any)["title"] = strings.Repeat("a", 201)
	r := e.do("PUT", "/courses/"+id+"/draft", "admin", bad)
	fields := r.JSON["errors"].([]any)[0].(map[string]any)["fields"].(map[string]any)
	if r.Code != 422 || fields["questions[0].options"] == nil || fields["lessons[0].title"] == nil {
		t.Fatalf("validación de borrador: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", "/courses/"+id+"/draft", "admin", `{"version":{"passingScore":80,"rowVersion":"1"}}`); r.Code != 422 {
		t.Fatalf("PUT sin listas no debe vaciar el borrador: %d", r.Code)
	}
	// Video pendiente: se acepta en el borrador, pero bloquea publicar.
	e.repo.media[uid(5, 2)] = domain.VideoFile{ID: uid(5, 2), OrganizationID: orgA, Status: "PENDING_UPLOAD"}
	saved := e.do("PUT", "/courses/"+id+"/draft", "admin", body("1", uid(5, 2)))
	if saved.Code != 200 || saved.data()["version"].(map[string]any)["rowVersion"] != "2" {
		t.Fatalf("guardar: %d %s", saved.Code, saved.Body)
	}
	if r := e.do("PUT", "/courses/"+id+"/draft", "admin", body("1", nil)); r.Code != 409 || r.errorCode() != "TRAINING_DRAFT_CONFLICT" {
		t.Fatalf("rowVersion antiguo: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/courses/"+id+"/publish", "admin", nil); r.Code != 422 || r.errorCode() != "TRAINING_DRAFT_INCOMPLETE" {
		t.Fatalf("video pendiente: %d %s", r.Code, r.Body)
	}
	ok := e.do("PUT", "/courses/"+id+"/draft", "admin", body(2, nil)) // rowVersion numérico también se acepta
	if ok.Code != 200 {
		t.Fatalf("%d %s", ok.Code, ok.Body)
	}
	pub := e.do("POST", "/courses/"+id+"/publish", "admin", nil)
	if pub.Code != 200 || pub.data()["course"].(map[string]any)["status"] != "PUBLISHED" || pub.data()["version"].(map[string]any)["versionNumber"].(float64) != 1 {
		t.Fatalf("publicar: %d %s", pub.Code, pub.Body)
	}
	if r := e.do("POST", "/courses/"+id+"/publish", "admin", nil); r.Code != 404 || r.errorCode() != "TRAINING_DRAFT_NOT_FOUND" {
		t.Fatalf("sin borrador: %d %s", r.Code, r.Body)
	}
	list := e.do("GET", "/courses?status=PUBLISHED", "admin", nil)
	items := list.data()["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["publishedVersionNumber"].(float64) != 1 || items[0].(map[string]any)["hasDraft"] != false {
		t.Fatalf("listado: %s", list.Body)
	}
	// Segunda edición: copia la versión publicada con identificadores nuevos; publicar archiva la anterior.
	copyDraft := e.do("GET", "/courses/"+id+"/draft", "admin", nil)
	copied := copyDraft.data()["lessons"].([]any)[0].(map[string]any)
	if copyDraft.data()["version"].(map[string]any)["versionNumber"].(float64) != 2 || copied["id"] == uid(3, 1) {
		t.Fatalf("copia del borrador: %s", copyDraft.Body)
	}
	if again := e.do("GET", "/courses/"+id+"/draft", "admin", nil); again.Body != copyDraft.Body {
		t.Fatal("GET del borrador debe ser estable cuando ya existe")
	}
	if r := e.do("POST", "/courses/"+id+"/publish", "admin", nil); r.Code != 200 || r.data()["version"].(map[string]any)["versionNumber"].(float64) != 2 {
		t.Fatalf("publicar v2: %d %s", r.Code, r.Body)
	}
	archived := 0
	for _, v := range e.repo.courses[id].versions {
		if v.status == domain.StatusArchived {
			archived++
		}
	}
	if archived != 1 {
		t.Fatalf("la versión anterior debe archivarse: %d", archived)
	}
}

func TestVideoReservationConfirmationAndSignedURLsStayOutOfLogs(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"id": uid(5, 1), "originalName": "clase.mp4", "mimeType": "video/mp4", "sizeBytes": 2048, "sha256": strings.Repeat("a", 64)}
	reserved := e.do("POST", "/videos/uploads", "admin", body)
	upload := reserved.data()["upload"].(map[string]any)
	file := reserved.data()["file"].(map[string]any)
	if reserved.Code != 201 || file["status"] != "PENDING_UPLOAD" || upload["method"] != "PUT" || upload["url"] != signedSecret {
		t.Fatalf("reserva: %d %s", reserved.Code, reserved.Body)
	}
	headers := upload["headers"].(map[string]any)
	if headers["Host"] != nil || headers["Content-Length"] != nil || headers["If-None-Match"] == nil || headers["Content-Type"] == nil {
		t.Fatalf("encabezados del PUT: %v", headers)
	}
	if again := e.do("POST", "/videos/uploads", "admin", body); again.Code != 201 || again.data()["file"].(map[string]any)["id"] != uid(5, 1) || again.data()["upload"] == nil {
		t.Fatalf("la repetición debe devolver la misma reserva con nueva firma: %d %s", again.Code, again.Body)
	}
	changed := map[string]any{"id": uid(5, 1), "originalName": "clase.mp4", "mimeType": "video/mp4", "sizeBytes": 4096, "sha256": strings.Repeat("a", 64)}
	if r := e.do("POST", "/videos/uploads", "admin", changed); r.Code != 409 || r.errorCode() != "TRAINING_VIDEO_CONFLICT" {
		t.Fatalf("metadatos distintos: %d %s", r.Code, r.Body)
	}
	for name, mutate := range map[string]func(map[string]any){
		"mime":   func(b map[string]any) { b["mimeType"] = "video/quicktime" },
		"tamaño": func(b map[string]any) { b["sizeBytes"] = 2 << 20 },
		"sha":    func(b map[string]any) { b["sha256"] = "abc" },
		"id":     func(b map[string]any) { b["id"] = "x" },
		"nombre": func(b map[string]any) { b["originalName"] = "../a.mp4" },
	} {
		bad := map[string]any{"id": uid(5, 2), "originalName": "a.mp4", "mimeType": "video/mp4", "sizeBytes": 10, "sha256": strings.Repeat("b", 64)}
		mutate(bad)
		if r := e.do("POST", "/videos/uploads", "admin", bad); r.Code != 422 {
			t.Fatalf("%s inválido debe ser 422: %d %s", name, r.Code, r.Body)
		}
	}

	// Aún no se subió: reintentable. Firma incorrecta: FAILED. Correcto: AVAILABLE.
	e.videos.inspectErr = contracts.ErrObjectNotFound
	if r := e.do("POST", "/videos/"+uid(5, 1)+"/confirm", "admin", nil); r.Code != 409 || r.errorCode() != "TRAINING_VIDEO_NOT_READY" {
		t.Fatalf("no subido: %d %s", r.Code, r.Body)
	}
	e.videos.inspectErr = nil
	e.videos.inspection = contracts.VideoInspection{ContentType: "video/mp4", ContentLength: 2048, SHA256Hex: strings.Repeat("a", 64), Header: []byte("<html><body>")}
	if r := e.do("POST", "/videos/"+uid(5, 1)+"/confirm", "admin", nil); r.Code != 422 || r.errorCode() != "TRAINING_VIDEO_INVALID" || e.repo.media[uid(5, 1)].Status != "FAILED" {
		t.Fatalf("firma falsa: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/videos/"+uid(5, 1)+"/confirm", "admin", nil); r.Code != 409 {
		t.Fatalf("un video FAILED no se confirma de nuevo: %d", r.Code)
	}

	// Un video válido con hash correcto queda AVAILABLE; con hash distinto, FAILED.
	good := map[string]any{"id": uid(5, 3), "originalName": "ok.webm", "mimeType": "video/webm", "sizeBytes": 100, "sha256": strings.Repeat("c", 64)}
	e.do("POST", "/videos/uploads", "admin", good)
	e.videos.inspection = contracts.VideoInspection{ContentType: "video/webm", ContentLength: 100, SHA256Hex: strings.Repeat("d", 64), Header: []byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0}}
	if r := e.do("POST", "/videos/"+uid(5, 3)+"/confirm", "admin", nil); r.Code != 422 {
		t.Fatalf("hash distinto debe fallar: %d %s", r.Code, r.Body)
	}
	good["id"] = uid(5, 4)
	e.do("POST", "/videos/uploads", "admin", good)
	e.videos.inspection = contracts.VideoInspection{ContentType: "video/webm", ContentLength: 100, SHA256Hex: strings.Repeat("c", 64), Header: []byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0}}
	ok := e.do("POST", "/videos/"+uid(5, 4)+"/confirm", "admin", nil)
	if ok.Code != 200 || ok.data()["file"].(map[string]any)["status"] != "AVAILABLE" {
		t.Fatalf("confirmar: %d %s", ok.Code, ok.Body)
	}
	if r := e.do("POST", "/videos/"+uid(5, 4)+"/confirm", "admin", nil); r.Code != 200 {
		t.Fatalf("confirmar es idempotente: %d", r.Code)
	}

	// Reproducción: URL firmada solo para miembros y nunca en logs.
	p := e.publish(true)
	played := e.do("POST", "/courses/"+p.course+"/lessons/"+p.lesson+"/video", "worker", nil)
	data := played.data()
	if played.Code != 200 || data["url"] != signedSecret || data["mimeType"] != "video/mp4" || data["expiresAt"] == nil {
		t.Fatalf("URL de video: %d %s", played.Code, played.Body)
	}
	if strings.Contains(e.logs.String(), "SECRET") || strings.Contains(e.logs.String(), "signed.example") {
		t.Fatalf("una URL firmada llegó a los logs: %s", e.logs.String())
	}
}

func TestInternalFailuresAreSanitizedAndStorageOutageIs503(t *testing.T) {
	e := newEnv(t)
	e.repo.fail = errors.New("postgres://user:secret@db/private")
	r := e.do("GET", "/courses", "admin", nil)
	if r.Code != 500 || r.errorCode() != "INTERNAL_ERROR" || strings.Contains(r.Body, "secret") || strings.Contains(e.logs.String(), "secret") {
		t.Fatalf("error interno filtrado: %d %s | %s", r.Code, r.Body, e.logs.String())
	}
	e.repo.fail = nil
	service := application.New(e.repo, nil, domain.VideoPolicy{MaxBytes: 1 << 20})
	router := chi.NewRouter()
	New(service, nil).Register(router, middleware.Authenticate(tokenVerifier{}))
	e.router = router
	body := map[string]any{"id": uid(5, 1), "originalName": "a.mp4", "mimeType": "video/mp4", "sizeBytes": 10, "sha256": strings.Repeat("a", 64)}
	if r := e.do("POST", "/videos/uploads", "admin", body); r.Code != 503 || r.errorCode() != "STORAGE_UNAVAILABLE" {
		t.Fatalf("R2 deshabilitado: %d %s", r.Code, r.Body)
	}
}
