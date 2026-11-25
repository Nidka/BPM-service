package traininghttp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// installBank instala los cinco módulos en orgA y devuelve el id del curso BPM-M01.
func (e *env) installBank() string {
	e.t.Helper()
	service := application.New(e.repo, e.videos, domain.VideoPolicy{MaxBytes: 1 << 20})
	if _, err := service.InstallBPMCatalog(context.Background(), orgA, "admin", false); err != nil {
		e.t.Fatal(err)
	}
	list := e.do("GET", "/courses", "admin", nil)
	for _, item := range list.data()["items"].([]any) {
		c := item.(map[string]any)
		if c["code"] == "BPM-M01" {
			return c["id"].(string)
		}
	}
	e.t.Fatal("no se instaló BPM-M01")
	return ""
}

func TestEvaluacionDelBancoNoRevelaClavesAntesDeResponder(t *testing.T) {
	e := newEnv(t)
	course := e.installBank()

	view := e.do("GET", "/courses/"+course, "worker", nil)
	if view.Code != 200 {
		t.Fatal(view.Body)
	}
	bank := view.data()["questionBank"].(map[string]any)
	if bank["version"].(float64) != 1 || bank["itemsPerAttempt"].(float64) != 5 || bank["poolSize"].(float64) != 20 {
		t.Fatal(bank)
	}
	for _, forbidden := range []string{"correctOption", "explanation", "itemCode", "bloom", "justification"} {
		if strings.Contains(view.Body, forbidden) {
			t.Fatalf("la vista de aprendizaje filtra %q", forbidden)
		}
	}
	versionID := view.data()["version"].(map[string]any)["id"].(string)

	// Sin iniciar la evaluación, el intento se rechaza.
	attemptBody := func(id string, answers []map[string]any) map[string]any {
		return map[string]any{"id": id, "clientOperationId": uid(8, len(id)+len(answers)), "versionId": versionID, "attemptType": "FINAL",
			"startedAt": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "clientCreatedAt": time.Now().UTC().Format(time.RFC3339), "answers": answers}
	}
	if r := e.do("POST", "/courses/"+course+"/attempts", "worker", attemptBody(uid(7, 1), []map[string]any{})); r.Code != 409 || r.errorCode() != "TRAINING_EVALUATION_NOT_STARTED" {
		t.Fatal(r.Code, r.Body)
	}
	// Autorización: solo quien tiene membresía inicia; otra organización no ve el curso.
	if r := e.do("POST", "/courses/"+course+"/evaluations", "root", map[string]any{"id": uid(7, 1), "attemptType": "FINAL"}); r.Code != 403 {
		t.Fatal("SUPER_ADMIN sin membresía no sortea", r.Code, r.Body)
	}
	if r := e.do("POST", "/courses/"+course+"/evaluations", "worker", map[string]any{"id": "x", "attemptType": "FINAL"}); r.Code != 422 {
		t.Fatal(r.Code, r.Body)
	}

	start := e.do("POST", "/courses/"+course+"/evaluations", "worker", map[string]any{"id": uid(7, 1), "attemptType": "FINAL"})
	if start.Code != 201 {
		t.Fatal(start.Code, start.Body)
	}
	for _, forbidden := range []string{"correctOption", "explanation", "itemCode", "bloom", "justification", "\"key\""} {
		if strings.Contains(start.Body, forbidden) {
			t.Fatalf("la evaluación filtra %q antes de responder", forbidden)
		}
	}
	evaluation := start.data()["evaluation"].(map[string]any)
	questions := evaluation["questions"].([]any)
	if len(questions) != 5 || evaluation["passingScore"].(float64) != 80 || evaluation["attemptNumber"].(float64) != 1 {
		t.Fatal(evaluation)
	}
	if again := e.do("POST", "/courses/"+course+"/evaluations", "worker", map[string]any{"id": uid(7, 1), "attemptType": "FINAL"}); again.Code != 200 || again.Body != start.Body {
		t.Fatal("repetir el inicio devuelve el mismo sorteo", again.Code)
	}

	// Califica con la alternativa correcta tal como se mostró, usando el sorteo guardado.
	draw := e.repo.bank.draws[uid(7, 1)]
	byID := map[string]domain.Question{}
	for _, c := range e.repo.courses {
		for _, v := range c.versions {
			for _, q := range v.questions {
				byID[q.ID] = q
			}
		}
	}
	answers := []map[string]any{}
	for i, item := range draw.Items {
		shown := 0
		for position, original := range item.OptionOrder {
			if original == byID[item.QuestionID].CorrectOption {
				shown = position
			}
		}
		if i == 0 { // una incorrecta: 4/5 sigue aprobando (80 %)
			shown = (shown + 1) % 5
		}
		answers = append(answers, map[string]any{"questionId": item.QuestionID, "selectedOption": shown})
	}
	done := e.do("POST", "/courses/"+course+"/attempts", "worker", attemptBody(uid(7, 1), answers))
	if done.Code != 201 {
		t.Fatal(done.Code, done.Body)
	}
	attempt := done.data()["attempt"].(map[string]any)
	if attempt["score"].(float64) != 80 || attempt["passed"] != true {
		t.Fatal("4/5 aprueba", attempt)
	}
	results := done.data()["results"].([]any)
	first := results[0].(map[string]any)
	sources := first["sources"].([]any)
	if first["itemCode"] == nil || first["explanation"] == "" || len(first["options"].([]any)) != 5 || len(sources) == 0 || sources[0].(map[string]any)["label"] == "" || sources[0].(map[string]any)["url"] == "" {
		t.Fatalf("el resultado debe traer ítem, justificación y fuentes: %v", first)
	}
	// El servidor registra ítem y versión por respuesta.
	for _, r := range e.repo.results[uid(7, 1)] {
		if r.ItemCode == "" || r.BankVersion != 1 || len(r.OptionOrder) != 5 {
			t.Fatalf("respuesta sin metadatos: %+v", r)
		}
	}
}

func TestPracticaDelBancoDaFuentesYRepaso(t *testing.T) {
	e := newEnv(t)
	course := e.installBank()
	view := e.do("GET", "/courses/"+course, "worker", nil)
	question := view.data()["questions"].([]any)[0].(map[string]any)["id"].(string)
	var key int
	for _, c := range e.repo.courses {
		for _, v := range c.versions {
			for _, q := range v.questions {
				if q.ID == question {
					key = q.CorrectOption
				}
			}
		}
	}
	wrong := e.do("POST", "/courses/"+course+"/study/answers", "worker", map[string]any{"questionId": question, "selectedOption": (key + 1) % 5})
	if wrong.Code != 200 || wrong.data()["correct"] != false || wrong.data()["itemCode"] == nil || len(wrong.data()["sources"].([]any)) == 0 {
		t.Fatal(wrong.Body)
	}
	review := e.do("GET", "/courses/"+course, "worker", nil).data()["review"].(map[string]any)
	if review["dueCount"].(float64) != 1 || review["questionIds"].([]any)[0] != question {
		t.Fatal(review)
	}
	right := e.do("POST", "/courses/"+course+"/study/answers", "worker", map[string]any{"questionId": question, "selectedOption": key})
	if right.Code != 200 || right.data()["xpEarned"].(float64) != 2 {
		t.Fatal(right.Body)
	}
}

func TestBancoParaAdministracionEsSoloLecturaYSoloAdmin(t *testing.T) {
	e := newEnv(t)
	course := e.installBank()
	if r := e.do("GET", "/courses/"+course+"/bank", "worker", nil); r.Code != 403 {
		t.Fatal("el personal no ve el banco", r.Code)
	}
	if r := e.do("GET", "/courses/"+course+"/bank", "chef", nil); r.Code != 403 {
		t.Fatal("el jefe de cocina no ve el banco", r.Code)
	}
	r := e.do("GET", "/courses/"+course+"/bank", "admin", nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	bank := r.data()["bank"].(map[string]any)
	items := bank["items"].([]any)
	if len(items) != 20 || bank["module"] != "M01" || bank["bankVersion"].(float64) != 1 {
		t.Fatal(bank["module"], len(items))
	}
	item := items[0].(map[string]any)
	for _, field := range []string{"code", "bloom", "key", "justification", "sources", "recommended", "questionText", "options"} {
		if item[field] == nil {
			t.Fatalf("falta %s en el ítem del banco", field)
		}
	}
	// Un curso común no tiene banco.
	plain := e.publish(false)
	if r = e.do("GET", "/courses/"+plain.course+"/bank", "admin", nil); r.Code != 422 || r.errorCode() != "TRAINING_NO_QUESTION_BANK" {
		t.Fatal(r.Code, r.Body)
	}
}
