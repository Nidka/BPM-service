package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Answer es una respuesta enviada por el cliente. Es la única parte de un
// intento que el cliente controla; puntaje, aprobación y XP los calcula el servidor.
type Answer struct {
	QuestionID     string
	SelectedOption int
}

// AttemptInput es la solicitud de un intento calificado.
type AttemptInput struct {
	ID, ClientOperationID, VersionID, AttemptType string
	StartedAt, ClientCreatedAt                    time.Time
	Answers                                       []Answer
}

// NormalizeAttempt valida el formato del intento y lo deja canónico (IDs en
// minúsculas, UTC con microsegundos y respuestas ordenadas por pregunta), de modo
// que dos reintentos equivalentes produzcan la misma huella.
func NormalizeAttempt(in AttemptInput, now time.Time) (AttemptInput, error) {
	fields := map[string]string{}
	in.ID = strings.ToLower(in.ID)
	in.ClientOperationID = strings.ToLower(in.ClientOperationID)
	in.VersionID = strings.ToLower(in.VersionID)
	for name, v := range map[string]string{"id": in.ID, "clientOperationId": in.ClientOperationID, "versionId": in.VersionID} {
		if !ValidID(v) {
			fields[name] = "Debe ser un identificador UUID válido."
		}
	}
	if in.AttemptType != AttemptDiagnostic && in.AttemptType != AttemptFinal {
		fields["attemptType"] = "El tipo de intento debe ser DIAGNOSTIC o FINAL."
	}
	if in.StartedAt.IsZero() {
		fields["startedAt"] = "Indica cuándo empezó el intento."
	}
	if in.ClientCreatedAt.IsZero() || in.ClientCreatedAt.After(now.Add(24*time.Hour)) {
		fields["clientCreatedAt"] = "La fecha del intento no es válida."
	}
	if len(in.Answers) > MaxQuestions {
		fields["answers"] = "Hay más respuestas que preguntas posibles."
	}
	if len(fields) > 0 {
		return in, invalid(fields)
	}
	in.StartedAt = in.StartedAt.UTC().Truncate(time.Microsecond)
	in.ClientCreatedAt = in.ClientCreatedAt.UTC().Truncate(time.Microsecond)
	answers := make([]Answer, len(in.Answers))
	for i, a := range in.Answers {
		a.QuestionID = strings.ToLower(a.QuestionID)
		answers[i] = a
	}
	sort.SliceStable(answers, func(i, j int) bool {
		if answers[i].QuestionID != answers[j].QuestionID {
			return answers[i].QuestionID < answers[j].QuestionID
		}
		return answers[i].SelectedOption < answers[j].SelectedOption
	})
	in.Answers = answers
	return in, nil
}

// Result es el resultado calificado de una pregunta.
type Result struct {
	QuestionID     string
	SelectedOption int
	Correct        bool
	CorrectOption  int
	Explanation    string
}

// Grade es el resultado completo de un intento.
type Grade struct {
	Score, CorrectCount, Total int
	Passed                     bool
	Results                    []Result
}

// ScorePercent devuelve round(correctas*100/total) con redondeo de .5 hacia arriba,
// calculado con enteros para evitar errores de coma flotante.
func ScorePercent(correct, total int) int {
	if total <= 0 {
		return 0
	}
	return (correct*200 + total) / (2 * total)
}

// GradeAttempt califica un intento contra todas las preguntas de la versión.
// Exige exactamente una respuesta por pregunta; cualquier pregunta ajena,
// repetida, faltante o con opción fuera de rango rechaza el intento completo
// con AnswersError y no se califica parcialmente.
func GradeAttempt(questions []Question, answers []Answer, passingScore int) (Grade, error) {
	fields := map[string]string{}
	byID := make(map[string]Question, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	chosen := make(map[string]int, len(answers))
	for i, a := range answers {
		id := strings.ToLower(a.QuestionID)
		q, known := byID[id]
		switch {
		case !known:
			fields[indexed("answers", i, "questionId")] = "La pregunta no pertenece a esta versión del curso."
		case func() bool { _, dup := chosen[id]; return dup }():
			fields[indexed("answers", i, "questionId")] = "La pregunta se respondió más de una vez."
		case a.SelectedOption < 0 || a.SelectedOption >= len(q.Options):
			fields[indexed("answers", i, "selectedOption")] = "La opción elegida está fuera de rango."
		default:
			chosen[id] = a.SelectedOption
		}
	}
	missing := 0
	for _, q := range questions {
		if _, ok := chosen[q.ID]; !ok {
			missing++
		}
	}
	if missing > 0 && len(fields) == 0 {
		fields["answers"] = fmt.Sprintf("Faltan %d pregunta(s) por responder.", missing)
	}
	if len(fields) > 0 {
		return Grade{}, &AnswersError{Fields: fields}
	}
	ordered := append([]Question(nil), questions...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].DisplayOrder < ordered[j].DisplayOrder })
	grade := Grade{Total: len(ordered), Results: make([]Result, 0, len(ordered))}
	for _, q := range ordered {
		selected := chosen[q.ID]
		ok := selected == q.CorrectOption
		if ok {
			grade.CorrectCount++
		}
		grade.Results = append(grade.Results, Result{QuestionID: q.ID, SelectedOption: selected, Correct: ok, CorrectOption: q.CorrectOption, Explanation: q.Explanation})
	}
	grade.Score = ScorePercent(grade.CorrectCount, grade.Total)
	grade.Passed = grade.Score >= passingScore
	return grade, nil
}

// StudyFeedback es la retroalimentación inmediata del modo estudio.
type StudyFeedback struct {
	Correct       bool
	CorrectOption int
	Explanation   string
}

// Study evalúa una opción sin calificar ni persistir. Una opción fuera de rango
// es un error de validación (HTTP 422).
func Study(q Question, selected int) (StudyFeedback, error) {
	if selected < 0 || selected >= len(q.Options) {
		return StudyFeedback{}, &ValidationError{Fields: map[string]string{"selectedOption": "La opción elegida está fuera de rango."}}
	}
	return StudyFeedback{Correct: selected == q.CorrectOption, CorrectOption: q.CorrectOption, Explanation: q.Explanation}, nil
}

// WithLesson registra la última lección vista sin retroceder un curso completado.
func (p Progress) WithLesson(lessonID string) Progress {
	p.LastLessonID = lessonID
	if p.Status == "" || p.Status == ProgressNotStarted {
		p.Status = ProgressInProgress
	}
	return p
}

// AfterAttempt aplica el efecto de un intento ya calificado en el servidor.
// DIAGNOSTIC solo mueve NOT_STARTED a IN_PROGRESS. FINAL actualiza la mejor nota;
// un FINAL aprobado completa el curso y acumula XP = 10 x correctas, conservando
// siempre el mayor XP obtenido. COMPLETED nunca retrocede.
func (p Progress) AfterAttempt(attemptType string, g Grade, now time.Time) Progress {
	if p.Status == "" {
		p.Status = ProgressNotStarted
	}
	if p.Status == ProgressNotStarted {
		p.Status = ProgressInProgress
	}
	if attemptType != AttemptFinal {
		return p
	}
	if p.BestScore == nil || g.Score > *p.BestScore {
		score := g.Score
		p.BestScore = &score
	}
	if g.Passed {
		p.Status = ProgressCompleted
		if p.CompletedAt == nil {
			completed := now.UTC()
			p.CompletedAt = &completed
		}
		if xp := g.CorrectCount * XPPerCorrectFinal; xp > p.ExperiencePoints {
			p.ExperiencePoints = xp
		}
	}
	return p
}
