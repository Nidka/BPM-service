package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func qid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func intp(v int) *int { return &v }

// capQuestions arma las 26 preguntas oficiales con identificadores predecibles.
func capQuestions() []Question {
	var qs []Question
	n := 0
	add := func(t string, correct *int, options []string) {
		n++
		section, _ := SectionFor(t)
		qs = append(qs, Question{ID: qid(n), Section: section, Type: t, Text: "p", Options: options, CorrectOption: correct, Order: n})
	}
	for i := 0; i < 8; i++ {
		add(TypeMultipleChoice, intp(1), []string{"a", "b", "c", "d"})
	}
	for i := 0; i < 8; i++ {
		add(TypeLikert, nil, LikertLabels)
	}
	for i := 0; i < 10; i++ {
		add(TypeFrequency, nil, FrequencyLabels)
	}
	return qs
}

func answersAll(qs []Question, mc, scale int) []AnswerInput {
	var out []AnswerInput
	for _, q := range qs {
		v := scale
		if q.Type == TypeMultipleChoice {
			v = mc
		}
		out = append(out, AnswerInput{QuestionID: q.ID, Value: v})
	}
	return out
}

func TestScoreCalculatesDimensions(t *testing.T) {
	qs := capQuestions()
	answers := answersAll(qs, 1, 4)
	answers[0].Value = 0 // una incorrecta
	answers[1].Value = 2 // otra incorrecta
	scored, s, err := Score(qs, answers)
	if err != nil {
		t.Fatal(err)
	}
	if s.KnowledgeCorrect != 6 || s.KnowledgeTotal != 8 || s.Knowledge != 75 {
		t.Fatalf("conocimiento incorrecto: %+v", s)
	}
	if s.Attitude != 4 || s.Practice != 4 {
		t.Fatalf("actitud/práctica incorrectas: %+v", s)
	}
	if len(scored) != 26 || scored[0].Correct == nil || *scored[0].Correct || scored[8].Correct != nil {
		t.Fatal("marcas de acierto incorrectas")
	}
}

func TestScoreRoundsMeanHalfUp(t *testing.T) {
	if got := ratio(37, 8); got != 4.63 { // 4.625
		t.Fatalf("ratio=%v", got)
	}
	if got := ratio(100, 3); got != 33.33 {
		t.Fatalf("ratio=%v", got)
	}
}

func TestScoreRejectsInvalidAnswers(t *testing.T) {
	qs := capQuestions()
	cases := map[string]func([]AnswerInput) []AnswerInput{
		"faltante":    func(a []AnswerInput) []AnswerInput { return a[:25] },
		"duplicada":   func(a []AnswerInput) []AnswerInput { a[1].QuestionID = a[0].QuestionID; return a },
		"desconocida": func(a []AnswerInput) []AnswerInput { a[0].QuestionID = qid(999); return a },
		"opción fuera de rango": func(a []AnswerInput) []AnswerInput {
			a[0].Value = 4
			return a
		},
		"likert cero":        func(a []AnswerInput) []AnswerInput { a[10].Value = 0; return a },
		"frecuencia sobre 5": func(a []AnswerInput) []AnswerInput { a[20].Value = 6; return a },
	}
	for name, mutate := range cases {
		_, _, err := Score(qs, mutate(answersAll(qs, 1, 3)))
		var ae *AnswersError
		if !errors.As(err, &ae) {
			t.Fatalf("%s: se esperaba AnswersError, hubo %v", name, err)
		}
	}
}

func TestCheckPublishableCAPComposition(t *testing.T) {
	qs := capQuestions()
	if err := CheckPublishable(qs, true); err != nil {
		t.Fatal(err)
	}
	var ie *IncompleteError
	if err := CheckPublishable(qs[1:], true); !errors.As(err, &ie) || !strings.Contains(ie.Fields["questions"], "8 de opción múltiple") {
		t.Fatalf("composición incorrecta debió fallar: %v", err)
	}
	// Un instrumento no CAP con otra composición sí puede publicarse.
	if err := CheckPublishable(qs[1:], false); err != nil {
		t.Fatal(err)
	}
	qs[0].CorrectOption = nil
	if err := CheckPublishable(qs, true); !errors.As(err, &ie) || ie.Fields["questions[0].correctOption"] == "" {
		t.Fatalf("sin respuesta correcta debió fallar: %v", err)
	}
	if err := CheckPublishable(nil, false); err == nil {
		t.Fatal("un borrador vacío no se publica")
	}
}

func TestNormalizeQuestions(t *testing.T) {
	out, err := NormalizeQuestions([]QuestionInput{
		{Text: " ¿Pregunta? ", Type: TypeMultipleChoice, Options: []string{" a ", "b"}},
		{Text: "Actitud", Type: TypeLikert},
		{Text: "Práctica", Type: TypeFrequency},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Text != "¿Pregunta?" || out[0].Options[0] != "a" || out[0].Section != SectionKnowledge || out[0].CorrectOption != nil {
		t.Fatalf("normalización MC: %+v", out[0])
	}
	if len(out[1].Options) != 5 || out[1].Section != SectionAttitude || out[2].Section != SectionPractice || out[2].Order != 3 {
		t.Fatal("escalas por defecto incorrectas")
	}
	bad := [][]QuestionInput{
		{{Text: "x", Type: "TEXT"}},
		{{Text: "", Type: TypeLikert}},
		{{Text: "x", Type: TypeMultipleChoice, Options: []string{"a"}}},
		{{Text: "x", Type: TypeMultipleChoice, Options: []string{"a", "A"}}},
		{{Text: "x", Type: TypeMultipleChoice, Options: []string{"a", "b"}, CorrectOption: intp(2)}},
		{{Text: "x", Type: TypeLikert, CorrectOption: intp(0)}},
		{{Text: "x", Type: TypeLikert, Options: []string{"a", "b"}}},
	}
	for i, b := range bad {
		var ve *ValidationError
		if _, err := NormalizeQuestions(b); !errors.As(err, &ve) {
			t.Fatalf("caso %d debió ser inválido", i)
		}
	}
}

func TestParticipantCodeIsPseudonymous(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewParticipantCode(nil)
		if err != nil || len(c) != 10 || !strings.HasPrefix(c, "P-") || seen[c] {
			t.Fatalf("código inválido o repetido: %q %v", c, err)
		}
		seen[c] = true
	}
}

func TestConsentValidity(t *testing.T) {
	var none *Consent
	if none.ValidFor("v1") {
		t.Fatal("sin registro no hay consentimiento")
	}
	if !(&Consent{DocumentVersion: "v1", Accepted: true}).ValidFor("v1") {
		t.Fatal("aceptado y vigente debe valer")
	}
	if (&Consent{DocumentVersion: "v1", Accepted: false}).ValidFor("v1") {
		t.Fatal("revocado no vale")
	}
	if (&Consent{DocumentVersion: "v1", Accepted: true}).ValidFor("v2") {
		t.Fatal("una versión distinta del documento exige nueva aceptación")
	}
}

func TestNormalizeSubmission(t *testing.T) {
	now := time.Now()
	ok := SubmissionInput{ID: qid(1), ClientOperationID: qid(2), VersionID: qid(3), Moment: MomentPretest, ClientCreatedAt: now, Answers: []AnswerInput{{QuestionID: qid(4)}}}
	if _, err := NormalizeSubmission(ok, now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*SubmissionInput){
		"momento":        func(s *SubmissionInput) { s.Moment = "MIDTEST" },
		"id":             func(s *SubmissionInput) { s.ID = "x" },
		"futuro":         func(s *SubmissionInput) { s.ClientCreatedAt = now.Add(time.Hour) },
		"sin respuestas": func(s *SubmissionInput) { s.Answers = nil },
	} {
		in := ok
		in.Answers = append([]AnswerInput(nil), ok.Answers...)
		mutate(&in)
		var ve *ValidationError
		if _, err := NormalizeSubmission(in, now); !errors.As(err, &ve) {
			t.Fatalf("%s debió ser inválido", name)
		}
	}
}

func TestNormalizeNeedAndConsentDocument(t *testing.T) {
	now := time.Now().UTC()
	n, err := NormalizeNeed(NeedInput{OrganizationID: "org-1", Category: "TRAINING", Description: " falta capacitación "}, now)
	if err != nil || n.Description != "falta capacitación" || !n.DetectedAt.Equal(now) {
		t.Fatalf("necesidad: %+v %v", n, err)
	}
	if _, err = NormalizeNeed(NeedInput{OrganizationID: "org-1", Category: "NOPE", Description: "x"}, now); err == nil {
		t.Fatal("categoría inválida")
	}
	if _, err = NormalizeConsentDocument(ConsentDocumentInput{Version: "v 1", Title: "t", Text: "x", ResponsibleInvestigator: "i"}); err == nil {
		t.Fatal("versión con espacio inválida")
	}
}
