package traininghttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (e *env) at(method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func assignmentBodyFor(id, course string, target map[string]any, due any) map[string]any {
	return map[string]any{"id": id, "courseId": course, "target": target, "dueAt": due}
}

func TestAssignmentPermissionsValidationAndMyList(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	worker := map[string]any{"type": "USER", "userId": "worker"}
	body := assignmentBodyFor(uid(9, 1), p.course, worker, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339))

	for _, user := range []string{"worker", "chef", "badmin"} {
		if r := e.do("POST", "/assignments", user, body); r.Code != 403 {
			t.Fatalf("%s no asigna: %d %s", user, r.Code, r.Body)
		}
		if r := e.do("GET", "/assignments", user, nil); r.Code != 403 {
			t.Fatalf("%s no lista: %d", user, r.Code)
		}
	}
	if r := e.do("POST", "/assignments", "", body); r.Code != 401 {
		t.Fatalf("sin sesión: %d", r.Code)
	}
	// Formato inválido, fecha pasada y campos extra.
	for _, bad := range []map[string]any{
		assignmentBodyFor("x", p.course, worker, nil),
		assignmentBodyFor(uid(9, 2), p.course, map[string]any{"type": "ALL"}, nil),
		assignmentBodyFor(uid(9, 2), p.course, worker, "2001-01-01T00:00:00Z"),
	} {
		if r := e.do("POST", "/assignments", "admin", bad); r.Code != 422 {
			t.Fatalf("debe ser 422: %d %s", r.Code, r.Body)
		}
	}
	if r := e.do("POST", "/assignments", "admin", `{"id":"x","extra":1}`); r.Code != 400 {
		t.Fatalf("campo extra: %d", r.Code)
	}
	// Otra empresa: curso inexistente en esta empresa y persona de otra empresa.
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 3), uid(9, 99), worker, nil)); r.Code != 404 || r.errorCode() != "TRAINING_COURSE_NOT_FOUND" {
		t.Fatalf("curso ajeno: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 3), p.course, map[string]any{"type": "USER", "userId": "badmin"}, nil)); r.Code != 404 || r.errorCode() != "TRAINING_ASSIGNEE_NOT_FOUND" {
		t.Fatalf("persona ajena: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 3), p.course, map[string]any{"type": "LOCATION", "locationId": uid(9, 50)}, nil)); r.Code != 404 || r.errorCode() != "TRAINING_LOCATION_NOT_FOUND" {
		t.Fatalf("sede ajena: %d %s", r.Code, r.Body)
	}

	created := e.do("POST", "/assignments", "admin", body)
	if created.Code != 201 || created.data()["assignment"].(map[string]any)["status"] != "ACTIVE" || e.repo.plus.ruleCalls != 1 {
		t.Fatalf("crear: %d %s", created.Code, created.Body)
	}
	if r := e.do("POST", "/assignments", "admin", body); r.Code != 200 {
		t.Fatalf("repetición idéntica: %d", r.Code)
	}
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 1), p.course, map[string]any{"type": "USER", "userId": "chef"}, nil)); r.Code != 409 || r.errorCode() != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("mismo id otro contenido: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 4), p.course, worker, nil)); r.Code != 409 || r.errorCode() != "TRAINING_ASSIGNMENT_EXISTS" {
		t.Fatalf("duplicado: %d %s", r.Code, r.Body)
	}

	mine := e.do("GET", "/me/assignments", "worker", nil)
	items := mine.data()["items"].([]any)
	if mine.Code != 200 || len(items) != 1 || items[0].(map[string]any)["status"] != "PENDING" {
		t.Fatalf("mis capacitaciones: %d %s", mine.Code, mine.Body)
	}
	if r := e.do("GET", "/me/assignments", "chef", nil); len(r.data()["items"].([]any)) != 0 {
		t.Fatalf("el chef no ve la asignación del trabajador: %s", r.Body)
	}
	if r := e.do("GET", "/me/assignments?tenant=x", "worker", nil); r.Code != 400 {
		t.Fatalf("parámetro desconocido: %d", r.Code)
	}
	if r := e.do("GET", "/me/assignments", "root", nil); r.Code != 403 {
		t.Fatalf("SUPER_ADMIN sin membresía: %d", r.Code)
	}
	if r := e.do("GET", "/assignments?status=BAD", "admin", nil); r.Code != 422 {
		t.Fatalf("filtro inválido: %d", r.Code)
	}
	if r := e.do("GET", "/assignments?status=ACTIVE", "admin", nil); r.Code != 200 || len(r.data()["items"].([]any)) != 1 {
		t.Fatalf("listado: %d %s", r.Code, r.Body)
	}

	id := created.data()["assignment"].(map[string]any)["id"].(string)
	if r := e.do("POST", "/assignments/"+id+"/cancel", "worker", nil); r.Code != 403 {
		t.Fatalf("cancelar sin permiso: %d", r.Code)
	}
	for i := 0; i < 2; i++ {
		if r := e.do("POST", "/assignments/"+id+"/cancel", "admin", nil); r.Code != 200 || r.data()["assignment"].(map[string]any)["status"] != "CANCELLED" {
			t.Fatalf("cancelar: %d %s", r.Code, r.Body)
		}
	}
	if r := e.do("POST", "/assignments/"+uid(9, 77)+"/cancel", "admin", nil); r.Code != 404 {
		t.Fatalf("inexistente: %d", r.Code)
	}
}

func TestModuleAttemptsAndAchievementsOverHTTP(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	path := "/courses/" + p.course + "/attempts"
	module := func(id string, lesson any, picks ...int) map[string]any {
		body := p.attempt(id, strings.Replace(id, "0000000", "0000001", 1), "MODULE", picks...)
		body["lessonId"] = lesson
		return body
	}
	if r := e.do("POST", path, "worker", module(uid(7, 1), uid(99, 1), 1, 1, 1)); r.Code != 404 || r.errorCode() != "TRAINING_LESSON_NOT_FOUND" {
		t.Fatalf("lección ajena: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", path, "worker", module(uid(7, 1), nil, 1, 1, 1)); r.Code != 422 {
		t.Fatalf("MODULE sin lección: %d %s", r.Code, r.Body)
	}
	first := e.do("POST", path, "worker", module(uid(7, 2), p.lesson, 1, 1, 1))
	attempt := first.data()["attempt"].(map[string]any)
	if first.Code != 201 || attempt["attemptType"] != "MODULE" || attempt["score"].(float64) != 100 || first.data()["certificate"] != nil ||
		first.data()["progress"].(map[string]any)["status"] != "IN_PROGRESS" {
		t.Fatalf("módulo: %d %s", first.Code, first.Body)
	}
	if ach := first.data()["achievements"].([]any); len(ach) != 1 || ach[0].(map[string]any)["code"] != "PERFECT_SCORE" {
		t.Fatalf("insignia: %s", first.Body)
	}
	if replay := e.do("POST", path, "worker", module(uid(7, 2), p.lesson, 1, 1, 1)); replay.Code != 200 || replay.Body != first.Body {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	// Reintentos con otros identificadores no repiten la insignia.
	again := e.do("POST", path, "worker", module(uid(7, 3), p.lesson, 1, 1, 1))
	if again.Code != 201 || len(again.data()["achievements"].([]any)) != 0 {
		t.Fatalf("no se repite: %s", again.Body)
	}
	if r := e.do("POST", path, "worker", p.attempt(uid(7, 4), uid(8, 4), "FINAL", 1, 1, 1)); len(r.data()["achievements"].([]any)) != 1 {
		t.Fatalf("FINAL otorga FIRST_COURSE: %s", r.Body)
	}
	me := e.do("GET", "/me/achievements", "worker", nil)
	totals := me.data()["totals"].(map[string]any)
	if me.Code != 200 || totals["experiencePoints"].(float64) != 30+15+50 || totals["achievementPoints"].(float64) != 50 {
		t.Fatalf("mis logros: %s", me.Body)
	}
	if r := e.do("GET", "/achievements", "worker", nil); r.Code != 403 {
		t.Fatalf("catálogo solo ADMIN: %d", r.Code)
	}
	cat := e.do("GET", "/achievements", "admin", nil)
	id := cat.data()["items"].([]any)[0].(map[string]any)["id"].(string)
	upd := map[string]any{"name": "Nuevo", "description": "", "points": 7, "criterionThreshold": 2, "active": true}
	if r := e.do("PUT", "/achievements/"+id, "worker", upd); r.Code != 403 {
		t.Fatalf("editar sin permiso: %d", r.Code)
	}
	if r := e.do("PUT", "/achievements/"+id, "admin", upd); r.Code != 200 || r.data()["achievement"].(map[string]any)["points"].(float64) != 7 {
		t.Fatalf("editar: %d %s", r.Code, r.Body)
	}
	upd["points"] = -3
	if r := e.do("PUT", "/achievements/"+id, "admin", upd); r.Code != 422 {
		t.Fatalf("puntos negativos: %d", r.Code)
	}
}

func TestPublicCertificateVerificationIsMinimalAndRateLimited(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	final := e.do("POST", "/courses/"+p.course+"/attempts", "worker", p.attempt(uid(7, 1), uid(8, 1), "FINAL", 1, 1, 1))
	code := final.data()["certificate"].(map[string]any)["verificationCode"].(string)

	get := func(c string) *httptest.ResponseRecorder { return e.at("GET", "/public/training-certificates/"+c, "") }
	ok := get(code)
	if ok.Code != 200 || ok.Header().Get("Cache-Control") != "no-store" || !strings.Contains(ok.Body.String(), `"status":"VALID"`) {
		t.Fatalf("válida sin autenticación: %d %s", ok.Code, ok.Body.String())
	}
	for _, leak := range []string{"worker", code, "userId", "certificate"} {
		if strings.Contains(ok.Body.String(), leak) {
			t.Fatalf("la verificación pública filtra %q: %s", leak, ok.Body.String())
		}
	}
	e.repo.plus.revoked[code] = true
	if rev := get(code); rev.Code != 200 || !strings.Contains(rev.Body.String(), `"status":"REVOKED"`) {
		t.Fatalf("revocada: %d %s", rev.Code, rev.Body.String())
	}
	// El tercer intento agota la ráfaga (3); los intentos inexistentes responden 404 igual que los inválidos.
	missing := get("AAAAAAAAAAAAAAAAAAAAAAAA")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "TRAINING_CERTIFICATE_NOT_FOUND") {
		t.Fatalf("inexistente: %d %s", missing.Code, missing.Body.String())
	}
	if limited := get("corto"); limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body.String(), "RATE_LIMITED") {
		t.Fatalf("límite de solicitudes: %d %s", limited.Code, limited.Body.String())
	}
}

func TestOfflineCatalogHasEtagAndNoAnswers(t *testing.T) {
	e := newEnv(t)
	p := e.publish(true)
	if r := e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 1), p.course, map[string]any{"type": "ROLE", "role": "WORKER"}, nil)); r.Code != 201 {
		t.Fatalf("asignar: %d %s", r.Code, r.Body)
	}
	get := func(token, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/organizations/"+orgA+"/training/me/offline-catalog", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		return rec
	}
	first := get("worker", "")
	etag := first.Header().Get("ETag")
	body := first.Body.String()
	if first.Code != 200 || etag == "" || !strings.Contains(body, `"offlineAvailable":true`) || !strings.Contains(body, `"sha256"`) || !strings.Contains(body, `"mediaId"`) {
		t.Fatalf("catálogo: %d %s", first.Code, body)
	}
	for _, leak := range []string{"correctOption", "explanation", "http", "objectKey", "signature"} {
		if strings.Contains(body, leak) {
			t.Fatalf("el catálogo expone %q", leak)
		}
	}
	if cached := get("worker", etag); cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("If-None-Match: %d", cached.Code)
	}
	if other := get("chef", etag); other.Code != 200 {
		t.Fatalf("el paquete del chef es distinto (vacío): %d", other.Code)
	}
	if r := get("root", ""); r.Code != 403 {
		t.Fatalf("SUPER_ADMIN: %d", r.Code)
	}
}

func TestExternalYouTubeVideoInDraftLearnViewAndOffline(t *testing.T) {
	e := newEnv(t)
	created := e.do("POST", "/courses", "admin", map[string]any{"code": "yt-1", "title": "Video externo"})
	course := created.data()["course"].(map[string]any)["id"].(string)
	draft := e.do("GET", "/courses/"+course+"/draft", "admin", nil)
	version := draft.data()["version"].(map[string]any)
	save := func(external any, media any) reply {
		lesson := map[string]any{"id": uid(3, 1), "title": "L", "content": "c", "displayOrder": 1, "videoMediaId": media, "externalVideo": external}
		question := map[string]any{"id": uid(2, 1), "lessonId": uid(3, 1), "questionText": "Q", "options": []string{"a", "b"}, "correctOption": 0, "explanation": "", "displayOrder": 1}
		return e.do("PUT", "/courses/"+course+"/draft", "admin", map[string]any{"version": map[string]any{"passingScore": 50, "rowVersion": version["rowVersion"]}, "lessons": []any{lesson}, "questions": []any{question}})
	}
	for _, bad := range []any{"https://evil.example/watch?v=dQw4w9WgXcQ", "https://www.youtube.com/watch?v=corto", 5,
		map[string]any{"provider": "VIMEO", "videoId": "dQw4w9WgXcQ"}, map[string]any{"provider": "YOUTUBE"},
		map[string]any{"provider": "YOUTUBE", "videoId": "dQw4w9WgXcQ", "url": "https://youtu.be/dQw4w9WgXcQ"}, map[string]any{"provider": "YOUTUBE", "videoId": "dQw4w9WgXcQ", "x": 1}} {
		if r := save(bad, nil); r.Code != 422 {
			t.Fatalf("%v debe ser 422: %d %s", bad, r.Code, r.Body)
		}
	}
	if r := save("https://youtu.be/dQw4w9WgXcQ", uid(5, 1)); r.Code != 422 {
		t.Fatalf("privado y externo a la vez: %d %s", r.Code, r.Body)
	}
	if r := e.do("PUT", "/courses/"+course+"/draft", "worker", map[string]any{}); r.Code != 403 {
		t.Fatalf("autorización antes que formato: %d", r.Code)
	}
	saved := save("https://www.youtube.com/shorts/dQw4w9WgXcQ", nil)
	if saved.Code != 200 {
		t.Fatalf("guardar: %d %s", saved.Code, saved.Body)
	}
	lesson := saved.data()["lessons"].([]any)[0].(map[string]any)
	if ext := lesson["externalVideo"].(map[string]any); ext["provider"] != "YOUTUBE" || ext["videoId"] != "dQw4w9WgXcQ" {
		t.Fatalf("normalizado a videoId: %v", ext)
	}
	if strings.Contains(saved.Body, "youtube.com") || strings.Contains(saved.Body, "shorts") {
		t.Fatal("nunca se devuelve una URL")
	}
	if r := e.do("POST", "/courses/"+course+"/publish", "admin", nil); r.Code != 200 {
		t.Fatalf("publicar con video externo: %d %s", r.Code, r.Body)
	}
	view := e.do("GET", "/courses/"+course, "worker", nil)
	if ext := view.data()["lessons"].([]any)[0].(map[string]any)["externalVideo"]; ext == nil {
		t.Fatalf("vista de aprendizaje: %s", view.Body)
	}
	e.do("POST", "/assignments", "admin", assignmentBodyFor(uid(9, 1), course, map[string]any{"type": "USER", "userId": "worker"}, nil))
	catalog := e.do("GET", "/me/offline-catalog", "worker", nil)
	if !strings.Contains(catalog.Body, `"offlineAvailable":false`) || strings.Contains(catalog.Body, "youtube.com") {
		t.Fatalf("catálogo offline: %s", catalog.Body)
	}
}

func TestPathsPermissionsAndValidationOverHTTP(t *testing.T) {
	e := newEnv(t)
	p := e.publish(false)
	body := map[string]any{"id": uid(9, 10), "title": "Ruta", "description": "d", "courseIds": []string{p.course}}
	if r := e.do("POST", "/paths", "worker", body); r.Code != 403 {
		t.Fatalf("trabajador no crea: %d", r.Code)
	}
	if r := e.do("POST", "/paths", "admin", map[string]any{"id": uid(9, 10), "title": "Ruta", "courseIds": []string{uid(9, 99)}}); r.Code != 422 {
		t.Fatalf("curso inexistente: %d %s", r.Code, r.Body)
	}
	created := e.do("POST", "/paths", "admin", body)
	if created.Code != 201 {
		t.Fatalf("crear: %d %s", created.Code, created.Body)
	}
	id := uid(9, 10)
	if r := e.do("GET", "/paths/"+id, "worker", nil); r.Code != 404 || r.errorCode() != "TRAINING_PATH_NOT_FOUND" {
		t.Fatalf("borrador oculto: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/paths/"+id+"/publish", "worker", nil); r.Code != 403 {
		t.Fatalf("publicar sin permiso: %d", r.Code)
	}
	if r := e.do("PUT", "/paths/"+id, "admin", map[string]any{"title": "Ruta", "courseIds": []string{p.course}, "rowVersion": "9"}); r.Code != 409 || r.errorCode() != "TRAINING_PATH_CONFLICT" {
		t.Fatalf("conflicto: %d %s", r.Code, r.Body)
	}
	if r := e.do("POST", "/paths/"+id+"/publish", "admin", nil); r.Code != 200 {
		t.Fatalf("publicar: %d %s", r.Code, r.Body)
	}
	view := e.do("GET", "/paths/"+id, "worker", nil)
	if view.Code != 200 || view.data()["path"].(map[string]any)["progress"].(map[string]any)["totalCourses"].(float64) != 1 {
		t.Fatalf("la ve quien aprende: %d %s", view.Code, view.Body)
	}
	if r := e.do("GET", "/paths?status=DRAFT", "worker", nil); r.Code != 403 {
		t.Fatalf("borradores solo ADMIN: %d", r.Code)
	}
	if r := e.do("GET", "/paths", "worker", nil); r.Code != 200 || len(r.data()["items"].([]any)) != 1 {
		t.Fatalf("listado: %d %s", r.Code, r.Body)
	}
}
