package domain

import (
	"crypto/rand"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// AnswerInput es la respuesta del cliente. Para opción múltiple Value es el índice
// 0-based de la opción elegida; para Likert y frecuencia es el valor 1 a 5.
type AnswerInput struct {
	QuestionID string
	Value      int
}

// ScoredAnswer es una respuesta ya validada; Correct solo existe en opción múltiple.
type ScoredAnswer struct {
	QuestionID string
	Value      int
	Correct    *bool
}

// Scores son los puntajes por dimensión calculados exclusivamente por el servidor.
// Knowledge es el porcentaje de aciertos (0 a 100); Attitude y Practice son la
// media de la escala de 1 a 5. Todos tienen dos decimales.
type Scores struct {
	KnowledgeCorrect, KnowledgeTotal int
	Knowledge, Attitude, Practice    float64
}

// SubmissionInput es una entrega del participante (PRETEST o POSTTEST).
type SubmissionInput struct {
	ID, ClientOperationID, VersionID, Moment string
	ClientCreatedAt                          time.Time
	Answers                                  []AnswerInput
}

// NormalizeSubmission valida el formato y normaliza identificadores en minúscula.
func NormalizeSubmission(in SubmissionInput, now time.Time) (SubmissionInput, error) {
	in.ID, in.ClientOperationID, in.VersionID = strings.ToLower(in.ID), strings.ToLower(in.ClientOperationID), strings.ToLower(in.VersionID)
	in.ClientCreatedAt = in.ClientCreatedAt.UTC()
	f := map[string]string{}
	if !ValidID(in.ID) {
		f["id"] = "Envía un UUID válido."
	}
	if !ValidID(in.ClientOperationID) {
		f["clientOperationId"] = "Envía un UUID válido."
	}
	if !ValidID(in.VersionID) {
		f["instrumentVersionId"] = "Envía el UUID de la versión del instrumento."
	}
	if !ValidMoment(in.Moment) {
		f["moment"] = "Usa PRETEST o POSTTEST."
	}
	if in.ClientCreatedAt.IsZero() || in.ClientCreatedAt.After(now.Add(5*time.Minute)) {
		f["clientCreatedAt"] = "La fecha de creación es obligatoria y no puede estar en el futuro."
	}
	if len(in.Answers) == 0 || len(in.Answers) > MaxQuestions {
		f["answers"] = "Envía una respuesta por pregunta."
	}
	for i := range in.Answers {
		in.Answers[i].QuestionID = strings.ToLower(in.Answers[i].QuestionID)
	}
	return in, invalid(f)
}

// Score valida que haya exactamente una respuesta por pregunta, dentro de rango, y
// calcula los puntajes por dimensión. No hay calificación parcial.
func Score(questions []Question, answers []AnswerInput) ([]ScoredAnswer, Scores, error) {
	byID := make(map[string]Question, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	f := map[string]string{}
	seen := map[string]bool{}
	scored := make([]ScoredAnswer, 0, len(answers))
	var s Scores
	var attSum, attN, prSum, prN int
	for i, a := range answers {
		q, ok := byID[a.QuestionID]
		switch {
		case !ok:
			f[indexed("answers", i, "questionId")] = "La pregunta no pertenece a esta versión."
			continue
		case seen[a.QuestionID]:
			f[indexed("answers", i, "questionId")] = "La pregunta se respondió más de una vez."
			continue
		}
		seen[a.QuestionID] = true
		answer := ScoredAnswer{QuestionID: a.QuestionID, Value: a.Value}
		switch q.Type {
		case TypeMultipleChoice:
			if a.Value < 0 || a.Value >= len(q.Options) {
				f[indexed("answers", i, "value")] = "Elige una opción válida."
				continue
			}
			ok := q.CorrectOption != nil && *q.CorrectOption == a.Value
			answer.Correct = &ok
			s.KnowledgeTotal++
			if ok {
				s.KnowledgeCorrect++
			}
		case TypeLikert, TypeFrequency:
			if a.Value < 1 || a.Value > ScaleSize {
				f[indexed("answers", i, "value")] = "Elige un valor de 1 a 5."
				continue
			}
			if q.Type == TypeLikert {
				attSum, attN = attSum+a.Value, attN+1
			} else {
				prSum, prN = prSum+a.Value, prN+1
			}
		}
		scored = append(scored, answer)
	}
	for _, q := range questions {
		if !seen[q.ID] {
			f["answers"] = "Responde todas las preguntas de la versión, cada una una vez."
			break
		}
	}
	if len(f) > 0 {
		return nil, Scores{}, &AnswersError{Fields: f}
	}
	s.Knowledge = ratio(s.KnowledgeCorrect*100, s.KnowledgeTotal)
	s.Attitude, s.Practice = ratio(attSum, attN), ratio(prSum, prN)
	return scored, s, nil
}

// ratio devuelve num/den con dos decimales, .5 hacia arriba, usando enteros.
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	scaled := (num*200 + den) / (2 * den)
	return float64(scaled) / 100
}

// CanonicalAnswers devuelve las respuestas ordenadas por pregunta para huellas estables.
func CanonicalAnswers(answers []AnswerInput) []AnswerInput {
	out := append([]AnswerInput(nil), answers...)
	sort.Slice(out, func(i, j int) bool { return out[i].QuestionID < out[j].QuestionID })
	return out
}

// codeAlphabet son 32 símbolos sin I ni O ni 0/1 para evitar ambigüedad al dictarlos.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewParticipantCode genera un código seudónimo "P-XXXXXXXX" con aleatoriedad
// criptográfica. No deriva de datos del usuario. random nil usa crypto/rand.
func NewParticipantCode(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	var raw [8]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("P-")
	for _, v := range raw {
		b.WriteByte(codeAlphabet[int(v)%len(codeAlphabet)])
	}
	return b.String(), nil
}

// ConsentDocumentInput es un documento de consentimiento enviado por un investigador.
type ConsentDocumentInput struct{ Version, Title, Text, ResponsibleInvestigator string }

// NormalizeConsentDocument valida el documento de consentimiento.
func NormalizeConsentDocument(in ConsentDocumentInput) (ConsentDocumentInput, error) {
	in.Version, in.Title, in.Text, in.ResponsibleInvestigator = clean(in.Version), clean(in.Title), clean(in.Text), clean(in.ResponsibleInvestigator)
	f := map[string]string{}
	if !versionPattern.MatchString(in.Version) {
		f["version"] = fmt.Sprintf("Usa de 1 a %d caracteres: letras, números, punto, guion o guion bajo.", MaxVersionLabel)
	}
	if !textOK(in.Title, 200) {
		f["title"] = "El título es obligatorio y admite hasta 200 caracteres."
	}
	if !textOK(in.Text, MaxConsentText) {
		f["text"] = fmt.Sprintf("El texto es obligatorio y admite hasta %d caracteres.", MaxConsentText)
	}
	if !textOK(in.ResponsibleInvestigator, 200) {
		f["responsibleInvestigator"] = "El investigador responsable es obligatorio (hasta 200 caracteres)."
	}
	return in, invalid(f)
}

// NeedInput es una necesidad detectada (RF-50).
type NeedInput struct {
	OrganizationID, Category, Description string
	DetectedAt                            time.Time
}

// NormalizeNeed valida la necesidad; DetectedAt vacío se reemplaza por now.
func NormalizeNeed(in NeedInput, now time.Time) (NeedInput, error) {
	in.Description = clean(in.Description)
	f := map[string]string{}
	if !ValidOrganization(in.OrganizationID) {
		f["organizationId"] = "Indica la empresa."
	}
	if !ValidNeedCategory(in.Category) {
		f["category"] = "Categoría no válida: " + strings.Join(NeedCategories, ", ") + "."
	}
	if !textOK(in.Description, MaxNeedDescription) {
		f["description"] = fmt.Sprintf("La descripción es obligatoria y admite hasta %d caracteres.", MaxNeedDescription)
	}
	if in.DetectedAt.IsZero() {
		in.DetectedAt = now
	}
	in.DetectedAt = in.DetectedAt.UTC()
	if in.DetectedAt.After(now.Add(5 * time.Minute)) {
		f["detectedAt"] = "La fecha de detección no puede estar en el futuro."
	}
	return in, invalid(f)
}
