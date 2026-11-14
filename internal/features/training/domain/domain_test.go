package domain

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func uid(kind, n int) string { return fmt.Sprintf("%08d-0000-0000-0000-%012d", kind, n) }

func questions(n int) []Question {
	out := make([]Question, n)
	for i := range out {
		out[i] = Question{ID: uid(2, i+1), Text: fmt.Sprintf("Pregunta %d", i+1), Options: []string{"a", "b", "c"}, CorrectOption: 1, Explanation: "porque sí", DisplayOrder: i + 1}
	}
	return out
}

// answersFor responde correctamente las primeras `correct` preguntas y mal el resto.
func answersFor(qs []Question, correct int) []Answer {
	out := make([]Answer, len(qs))
	for i, q := range qs {
		pick := q.CorrectOption
		if i >= correct {
			pick = (q.CorrectOption + 1) % len(q.Options)
		}
		out[i] = Answer{QuestionID: q.ID, SelectedOption: pick}
	}
	return out
}

func TestScorePercentRoundsHalfUp(t *testing.T) {
	for _, tc := range []struct{ c, n, want int }{
		{0, 3, 0}, {1, 3, 33}, {2, 3, 67}, {3, 3, 100}, {1, 8, 13}, {9, 10, 90}, {1, 2, 50}, {0, 0, 0},
	} {
		if got := ScorePercent(tc.c, tc.n); got != tc.want {
			t.Errorf("ScorePercent(%d,%d)=%d, esperado %d", tc.c, tc.n, got, tc.want)
		}
	}
}

func TestGradeAttemptPassingBoundaryIsInclusive(t *testing.T) {
	qs := questions(10)
	at80, err := GradeAttempt(qs, answersFor(qs, 8), 80)
	if err != nil || at80.Score != 80 || !at80.Passed {
		t.Fatalf("80 con mínimo 80 debe aprobar: %+v %v", at80, err)
	}
	at70, err := GradeAttempt(qs, answersFor(qs, 7), 80)
	if err != nil || at70.Score != 70 || at70.Passed {
		t.Fatalf("70 con mínimo 80 no aprueba: %+v %v", at70, err)
	}
	// El redondeo cuenta: 2 de 3 = 67 no aprueba 67+1 pero sí 67.
	three := questions(3)
	g, _ := GradeAttempt(three, answersFor(three, 2), 67)
	if g.Score != 67 || !g.Passed {
		t.Fatalf("el límite se evalúa con el puntaje redondeado: %+v", g)
	}
}

func TestGradeAttemptReturnsOrderedResultsWithAnswers(t *testing.T) {
	qs := questions(3)
	answers := answersFor(qs, 2)
	// Orden de entrada distinto al orden de las preguntas.
	answers[0], answers[2] = answers[2], answers[0]
	g, err := GradeAttempt(qs, answers, 50)
	if err != nil || g.CorrectCount != 2 || g.Total != 3 {
		t.Fatal(err, g)
	}
	for i, r := range g.Results {
		if r.QuestionID != qs[i].ID || r.CorrectOption != 1 || r.Explanation != "porque sí" {
			t.Fatalf("resultado fuera de orden o sin respuesta: %+v", r)
		}
	}
	if g.Results[2].Correct {
		t.Fatal("la tercera pregunta se respondió mal")
	}
}

func TestGradeAttemptRejectsTamperedDuplicatedOrMissingAnswers(t *testing.T) {
	qs := questions(3)
	valid := answersFor(qs, 3)
	for name, answers := range map[string][]Answer{
		"faltante":     valid[:2],
		"vacío":        nil,
		"duplicada":    {valid[0], valid[1], valid[1]},
		"ajena":        {valid[0], valid[1], {QuestionID: uid(9, 9), SelectedOption: 0}},
		"fuera rango":  {valid[0], valid[1], {QuestionID: qs[2].ID, SelectedOption: 3}},
		"negativa":     {valid[0], valid[1], {QuestionID: qs[2].ID, SelectedOption: -1}},
		"duplicada+ok": {valid[0], valid[1], valid[2], valid[0]},
	} {
		_, err := GradeAttempt(qs, answers, 80)
		var invalid *AnswersError
		if !errors.As(err, &invalid) || len(invalid.Fields) == 0 {
			t.Fatalf("%s: debía rechazarse con detalle, obtuvo %v", name, err)
		}
	}
}

func TestNormalizeAttemptCanonicalizesAndValidates(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	in := AttemptInput{ID: strings.ToUpper(uid(1, 1)), ClientOperationID: uid(1, 2), VersionID: uid(1, 3), AttemptType: AttemptFinal,
		StartedAt: now.Add(-time.Minute), ClientCreatedAt: now,
		Answers: []Answer{{QuestionID: uid(2, 2), SelectedOption: 1}, {QuestionID: uid(2, 1), SelectedOption: 0}}}
	out, err := NormalizeAttempt(in, now)
	if err != nil || out.ID != uid(1, 1) || out.Answers[0].QuestionID != uid(2, 1) {
		t.Fatal("no canonicalizó", err, out)
	}
	for name, mutate := range map[string]func(*AttemptInput){
		"tipo":      func(a *AttemptInput) { a.AttemptType = "MODULE" },
		"id":        func(a *AttemptInput) { a.ID = "x" },
		"operación": func(a *AttemptInput) { a.ClientOperationID = "" },
		"versión":   func(a *AttemptInput) { a.VersionID = "x" },
		"fecha":     func(a *AttemptInput) { a.ClientCreatedAt = time.Time{} },
		"futuro":    func(a *AttemptInput) { a.ClientCreatedAt = now.Add(48 * time.Hour) },
		"inicio":    func(a *AttemptInput) { a.StartedAt = time.Time{} },
	} {
		bad := in
		mutate(&bad)
		var v *ValidationError
		if _, err := NormalizeAttempt(bad, now); !errors.As(err, &v) {
			t.Fatalf("%s: se esperaba error de validación", name)
		}
	}
}

func TestProgressTransitionsAndXP(t *testing.T) {
	now := time.Now()
	passed := Grade{Score: 90, CorrectCount: 9, Total: 10, Passed: true}
	failed := Grade{Score: 40, CorrectCount: 4, Total: 10}

	p := Progress{}.AfterAttempt(AttemptDiagnostic, passed, now)
	if p.Status != ProgressInProgress || p.BestScore != nil || p.ExperiencePoints != 0 {
		t.Fatalf("DIAGNOSTIC solo inicia el curso: %+v", p)
	}
	p = p.AfterAttempt(AttemptFinal, failed, now)
	if p.Status != ProgressInProgress || *p.BestScore != 40 || p.ExperiencePoints != 0 || p.CompletedAt != nil {
		t.Fatalf("FINAL reprobado no completa ni otorga XP: %+v", p)
	}
	p = p.AfterAttempt(AttemptFinal, passed, now)
	if p.Status != ProgressCompleted || *p.BestScore != 90 || p.ExperiencePoints != 90 || p.CompletedAt == nil {
		t.Fatalf("FINAL aprobado completa y otorga 10 x correctas: %+v", p)
	}
	// Un intento posterior peor nunca retrocede estado, nota ni XP.
	p = p.AfterAttempt(AttemptFinal, failed, now.Add(time.Hour))
	if p.Status != ProgressCompleted || *p.BestScore != 90 || p.ExperiencePoints != 90 {
		t.Fatalf("COMPLETED no retrocede: %+v", p)
	}
	// Un aprobado con menos aciertos no reduce el XP.
	p = p.AfterAttempt(AttemptFinal, Grade{Score: 80, CorrectCount: 8, Passed: true}, now)
	if p.ExperiencePoints != 90 {
		t.Fatalf("el XP conserva el máximo: %+v", p)
	}
	if got := (Progress{Status: ProgressCompleted}).WithLesson(uid(3, 1)); got.Status != ProgressCompleted || got.LastLessonID != uid(3, 1) {
		t.Fatal("WithLesson no debe retroceder COMPLETED")
	}
	if got := (Progress{}).WithLesson(uid(3, 1)); got.Status != ProgressInProgress {
		t.Fatal("WithLesson debe iniciar el curso")
	}
}

func TestStudyGivesFeedbackWithoutScoring(t *testing.T) {
	q := questions(1)[0]
	ok, err := Study(q, 1)
	if err != nil || !ok.Correct || ok.CorrectOption != 1 || ok.Explanation == "" {
		t.Fatal(ok, err)
	}
	bad, _ := Study(q, 0)
	if bad.Correct || bad.CorrectOption != 1 {
		t.Fatal("la opción incorrecta debe revelar la correcta tras responder")
	}
	var v *ValidationError
	for _, option := range []int{-1, 3} {
		if _, err := Study(q, option); !errors.As(err, &v) {
			t.Fatalf("la opción %d está fuera de rango", option)
		}
	}
}

func validDraft() DraftInput {
	return DraftInput{PassingScore: 80, RowVersion: 1,
		Lessons:   []Lesson{{ID: uid(3, 1), Title: "Lavado", Content: "Texto\ncon salto", DisplayOrder: 2}, {ID: uid(3, 2), Title: "Higiene", DisplayOrder: 1}},
		Questions: []Question{{ID: uid(2, 1), LessonID: uid(3, 1), Text: "¿Cuándo?", Options: []string{" Siempre ", "Nunca"}, CorrectOption: 0, DisplayOrder: 1}}}
}

func TestNormalizeDraftAcceptsAndRenumbers(t *testing.T) {
	out, err := NormalizeDraft(validDraft())
	if err != nil {
		t.Fatal(err)
	}
	if out.Lessons[0].ID != uid(3, 2) || out.Lessons[0].DisplayOrder != 1 || out.Lessons[1].DisplayOrder != 2 {
		t.Fatalf("debe ordenar y renumerar: %+v", out.Lessons)
	}
	if out.Questions[0].Options[0] != "Siempre" {
		t.Fatal("debe recortar opciones")
	}
}

func TestNormalizeDraftRejectsInvalidContent(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := map[string]struct {
		mutate func(*DraftInput)
		field  string
	}{
		"puntaje":        {func(d *DraftInput) { d.PassingScore = 0 }, "version.passingScore"},
		"puntaje alto":   {func(d *DraftInput) { d.PassingScore = 101 }, "version.passingScore"},
		"rowVersion":     {func(d *DraftInput) { d.RowVersion = 0 }, "version.rowVersion"},
		"título largo":   {func(d *DraftInput) { d.Lessons[0].Title = long(201) }, "lessons[0].title"},
		"título vacío":   {func(d *DraftInput) { d.Lessons[0].Title = "  " }, "lessons[0].title"},
		"contenido":      {func(d *DraftInput) { d.Lessons[0].Content = long(20001) }, "lessons[0].content"},
		"id lección":     {func(d *DraftInput) { d.Lessons[1].ID = d.Lessons[0].ID }, "lessons[1].id"},
		"orden repetido": {func(d *DraftInput) { d.Lessons[1].DisplayOrder = 2 }, "lessons[1].displayOrder"},
		"video":          {func(d *DraftInput) { d.Lessons[0].VideoMediaID = "no-uuid" }, "lessons[0].videoMediaId"},
		"una opción":     {func(d *DraftInput) { d.Questions[0].Options = []string{"a"} }, "questions[0].options"},
		"siete opciones": {func(d *DraftInput) { d.Questions[0].Options = []string{"1", "2", "3", "4", "5", "6", "7"} }, "questions[0].options"},
		"opción larga":   {func(d *DraftInput) { d.Questions[0].Options = []string{"a", long(301)} }, "questions[0].options"},
		"repetidas":      {func(d *DraftInput) { d.Questions[0].Options = []string{"A", " a "} }, "questions[0].options"},
		"correcta":       {func(d *DraftInput) { d.Questions[0].CorrectOption = 2 }, "questions[0].correctOption"},
		"lección ajena":  {func(d *DraftInput) { d.Questions[0].LessonID = uid(3, 9) }, "questions[0].lessonId"},
		"enunciado":      {func(d *DraftInput) { d.Questions[0].Text = "" }, "questions[0].questionText"},
		"id pregunta":    {func(d *DraftInput) { d.Questions[0].ID = "x" }, "questions[0].id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			draft := validDraft()
			tc.mutate(&draft)
			var v *ValidationError
			_, err := NormalizeDraft(draft)
			if !errors.As(err, &v) || v.Fields[tc.field] == "" {
				t.Fatalf("se esperaba error en %s, obtuvo %v", tc.field, err)
			}
		})
	}
	tooMany := validDraft()
	tooMany.Lessons = make([]Lesson, MaxLessons+1)
	if _, err := NormalizeDraft(tooMany); err == nil {
		t.Fatal("más de 50 lecciones debe rechazarse")
	}
	tooMany = validDraft()
	tooMany.Questions = make([]Question, MaxQuestions+1)
	if _, err := NormalizeDraft(tooMany); err == nil {
		t.Fatal("más de 100 preguntas debe rechazarse")
	}
}

func TestValidateForPublishRequiresContentAndAvailableVideos(t *testing.T) {
	draft := Draft{Lessons: []Lesson{{ID: uid(3, 1), VideoMediaID: uid(4, 1)}}, Questions: questions(1)}
	if err := draft.ValidateForPublish(map[string]string{uid(4, 1): "AVAILABLE"}); err != nil {
		t.Fatal(err)
	}
	for name, status := range map[string]map[string]string{"pendiente": {uid(4, 1): "PENDING_UPLOAD"}, "fallido": {uid(4, 1): "FAILED"}, "ajeno": {}} {
		var incomplete *IncompleteError
		if err := draft.ValidateForPublish(status); !errors.As(err, &incomplete) || incomplete.Fields["lessons[0].videoMediaId"] == "" {
			t.Fatalf("%s: video no disponible debe bloquear", name)
		}
	}
	var incomplete *IncompleteError
	if err := (Draft{}).ValidateForPublish(nil); !errors.As(err, &incomplete) || incomplete.Fields["lessons"] == "" || incomplete.Fields["questions"] == "" {
		t.Fatal("un borrador vacío no se publica")
	}
}

func TestVideoPolicyAllowsOnlyMP4AndWebM(t *testing.T) {
	policy := VideoPolicy{MaxBytes: 1000}
	valid := VideoUploadInput{ID: uid(5, 1), OriginalName: "clase.mp4", MIMEType: "video/mp4", SizeBytes: 1000, SHA256: strings.Repeat("a", 64)}
	if _, err := policy.Validate(valid); err != nil {
		t.Fatal(err)
	}
	valid.MIMEType = "VIDEO/WEBM"
	if out, err := policy.Validate(valid); err != nil || out.MIMEType != "video/webm" {
		t.Fatal("WebM debe aceptarse", err)
	}
	for field, mutate := range map[string]func(*VideoUploadInput){
		"mimeType":     func(v *VideoUploadInput) { v.MIMEType = "video/quicktime" },
		"sizeBytes":    func(v *VideoUploadInput) { v.SizeBytes = 1001 },
		"sha256":       func(v *VideoUploadInput) { v.SHA256 = "abc" },
		"originalName": func(v *VideoUploadInput) { v.OriginalName = "../x.mp4" },
		"id":           func(v *VideoUploadInput) { v.ID = "1" },
	} {
		bad := valid
		bad.MIMEType = "video/mp4"
		mutate(&bad)
		var v *ValidationError
		if _, err := policy.Validate(bad); !errors.As(err, &v) || v.Fields[field] == "" {
			t.Fatalf("%s debe rechazarse: %v", field, err)
		}
	}
	zero := valid
	zero.SizeBytes = 0
	if _, err := policy.Validate(zero); err == nil {
		t.Fatal("tamaño cero debe rechazarse")
	}
}

func TestValidVideoSignature(t *testing.T) {
	mp4 := []byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	webm := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81}
	for name, tc := range map[string]struct {
		mime   string
		header []byte
		want   bool
	}{
		"mp4":              {MIMEVideoMP4, mp4, true},
		"webm":             {MIMEVideoWebM, webm, true},
		"mp4 declara webm": {MIMEVideoWebM, mp4, false},
		"webm declara mp4": {MIMEVideoMP4, webm, false},
		"html":             {MIMEVideoMP4, []byte("<html><body>fake</body></html>"), false},
		"mp4 corto":        {MIMEVideoMP4, mp4[:6], false},
		"webm corto":       {MIMEVideoWebM, webm[:3], false},
		"vacío":            {MIMEVideoMP4, nil, false},
		"mime desconocido": {"video/quicktime", mp4, false},
		"ftyp mal offset":  {MIMEVideoMP4, []byte{'f', 't', 'y', 'p', 0, 0, 0, 0}, false},
	} {
		if got := ValidVideoSignature(tc.mime, tc.header); got != tc.want {
			t.Errorf("%s: obtuvo %v", name, got)
		}
	}
}

func TestVerificationCodeIsRandomAndLongEnough(t *testing.T) {
	a, err := NewVerificationCode(nil)
	b, _ := NewVerificationCode(nil)
	if err != nil || len(a) < 16 || a == b {
		t.Fatalf("código débil: %q %q %v", a, b, err)
	}
	for _, r := range a {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", r) {
			t.Fatalf("carácter fuera de base32: %q", r)
		}
	}
	fixed, _ := NewVerificationCode(bytes.NewReader(bytes.Repeat([]byte{0}, 15)))
	if fixed != strings.Repeat("A", 24) {
		t.Fatalf("el código debe derivar del lector: %s", fixed)
	}
	if _, err := NewVerificationCode(bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("un lector corto debe fallar, no producir un código débil")
	}
}

func TestVideoFileMatchAndKey(t *testing.T) {
	in := VideoUploadInput{ID: uid(5, 1), OriginalName: "a.mp4", MIMEType: MIMEVideoMP4, SizeBytes: 5, SHA256: strings.Repeat("b", 64)}
	file := VideoFile{ID: in.ID, OriginalName: in.OriginalName, MIMEType: in.MIMEType, SizeBytes: 5, SHA256: in.SHA256}
	if file.Match(in) != nil {
		t.Fatal("metadatos idénticos deben coincidir")
	}
	in.SizeBytes = 6
	if !errors.Is(file.Match(in), ErrVideoConflict) {
		t.Fatal("metadatos distintos son conflicto")
	}
	if got := VideoObjectKey("org", uid(5, 1), MIMEVideoWebM); got != "organizations/org/training/videos/"+uid(5, 1)+".webm" {
		t.Fatal(got)
	}
}

func TestNewCourseNormalize(t *testing.T) {
	minutes := 15
	out, err := NewCourse{Code: " m-01 ", Title: " Manos ", EstimatedDurationMinutes: &minutes}.Normalize()
	if err != nil || out.Code != "M-01" || out.Title != "Manos" {
		t.Fatal(out, err)
	}
	zero := 0
	for _, bad := range []NewCourse{{Code: "", Title: "Manos"}, {Code: "a b", Title: "Manos"}, {Code: "M1", Title: "x"}, {Code: "M1", Title: "Manos", EstimatedDurationMinutes: &zero}} {
		if _, err := bad.Normalize(); err == nil {
			t.Fatalf("debe rechazar %+v", bad)
		}
	}
}

func TestActorPermissions(t *testing.T) {
	for role, want := range map[string][3]bool{"ADMIN": {true, true, true}, "CHEF": {true, true, false}, "WORKER": {true, true, false}, "SUPER_ADMIN": {true, false, true}, "": {false, false, false}} {
		a := Actor{Role: role}
		if a.CanLearn() != want[0] || a.CanPersist() != want[1] || a.CanAuthor() != want[2] {
			t.Errorf("permisos incorrectos para %q", role)
		}
	}
}

func TestNormalizeDraftDistinguishesMissingFromEmptyLists(t *testing.T) {
	empty := DraftInput{PassingScore: 80, RowVersion: 1, Lessons: []Lesson{}, Questions: []Question{}}
	if _, err := NormalizeDraft(empty); err != nil {
		t.Fatalf("listas vacías son válidas para guardar: %v", err)
	}
	missing := empty
	missing.Lessons, missing.Questions = nil, nil
	var v *ValidationError
	if _, err := NormalizeDraft(missing); !errors.As(err, &v) || v.Fields["lessons"] == "" || v.Fields["questions"] == "" {
		t.Fatal("listas ausentes no deben vaciar el borrador")
	}
}
