package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// QuestionInput es una pregunta enviada por un investigador. La dimensión se
// deriva del tipo y el orden del arreglo define display_order.
type QuestionInput struct {
	Text          string
	Type          string
	Options       []string
	CorrectOption *int
}

// InstrumentInput es la cabecera enviada al crear o importar un instrumento.
type InstrumentInput struct {
	Code, Name, Description string
	IsCAP                   bool
}

// Etiquetas predeterminadas de las escalas de cinco puntos (valores 1 a 5).
var (
	LikertLabels    = []string{"Totalmente en desacuerdo", "En desacuerdo", "Ni de acuerdo ni en desacuerdo", "De acuerdo", "Totalmente de acuerdo"}
	FrequencyLabels = []string{"Nunca", "Casi nunca", "A veces", "Casi siempre", "Siempre"}
)

func clean(v string) string { return strings.TrimSpace(v) }

func hasControl(v string) bool {
	return strings.IndexFunc(v, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0
}

// textOK valida texto no vacío, UTF-8, sin controles y de hasta max caracteres.
func textOK(v string, max int) bool {
	n := utf8.RuneCountInString(v)
	return n >= 1 && n <= max && utf8.ValidString(v) && !hasControl(v)
}

// NormalizeInstrument valida y normaliza la cabecera de un instrumento.
func NormalizeInstrument(in InstrumentInput) (InstrumentInput, error) {
	in.Code, in.Name, in.Description = strings.ToUpper(clean(in.Code)), clean(in.Name), clean(in.Description)
	f := map[string]string{}
	if !codePattern.MatchString(in.Code) {
		f["code"] = "Usa de 1 a 50 caracteres: mayúsculas, números, guion o guion bajo."
	}
	if !textOK(in.Name, MaxInstrumentName) {
		f["name"] = fmt.Sprintf("El nombre es obligatorio y admite hasta %d caracteres.", MaxInstrumentName)
	}
	if in.Description != "" && !textOK(in.Description, MaxDescription) {
		f["description"] = fmt.Sprintf("La descripción admite hasta %d caracteres.", MaxDescription)
	}
	return in, invalid(f)
}

// NormalizeQuestions valida las preguntas de un borrador y les asigna dimensión y orden.
// La respuesta correcta puede faltar mientras es borrador; CheckPublishable la exige.
func NormalizeQuestions(in []QuestionInput) ([]Question, error) {
	f := map[string]string{}
	if len(in) > MaxQuestions {
		f["questions"] = fmt.Sprintf("Un instrumento admite hasta %d preguntas.", MaxQuestions)
		return nil, invalid(f)
	}
	out := make([]Question, 0, len(in))
	for i, q := range in {
		section, ok := SectionFor(q.Type)
		text := clean(q.Text)
		if !ok {
			f[indexed("questions", i, "type")] = "Usa MULTIPLE_CHOICE, LIKERT o FREQUENCY."
		}
		if !textOK(text, MaxQuestionText) {
			f[indexed("questions", i, "text")] = fmt.Sprintf("El enunciado es obligatorio y admite hasta %d caracteres.", MaxQuestionText)
		}
		options, correct := normalizeOptions(q, i, f)
		out = append(out, Question{Section: section, Type: q.Type, Text: text, Options: options, CorrectOption: correct, Order: i + 1})
	}
	return out, invalid(f)
}

func normalizeOptions(q QuestionInput, i int, f map[string]string) ([]string, *int) {
	options := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		options = append(options, clean(o))
	}
	switch q.Type {
	case TypeMultipleChoice:
		seen := map[string]bool{}
		if len(options) < MinOptions || len(options) > MaxOptions {
			f[indexed("questions", i, "options")] = fmt.Sprintf("Define de %d a %d opciones.", MinOptions, MaxOptions)
		}
		for _, o := range options {
			key := strings.ToLower(o)
			if !textOK(o, MaxOptionLength) || seen[key] {
				f[indexed("questions", i, "options")] = fmt.Sprintf("Cada opción es obligatoria, única y de hasta %d caracteres.", MaxOptionLength)
				break
			}
			seen[key] = true
		}
		if q.CorrectOption != nil && (*q.CorrectOption < 0 || *q.CorrectOption >= len(options)) {
			f[indexed("questions", i, "correctOption")] = "La respuesta correcta debe ser el índice de una opción."
		}
		return options, q.CorrectOption
	case TypeLikert, TypeFrequency:
		if q.CorrectOption != nil {
			f[indexed("questions", i, "correctOption")] = "Solo las preguntas de opción múltiple tienen respuesta correcta."
		}
		if len(options) == 0 {
			if q.Type == TypeLikert {
				return append([]string(nil), LikertLabels...), nil
			}
			return append([]string(nil), FrequencyLabels...), nil
		}
		if len(options) != ScaleSize {
			f[indexed("questions", i, "options")] = fmt.Sprintf("La escala tiene exactamente %d etiquetas.", ScaleSize)
		}
		for _, o := range options {
			if !textOK(o, MaxScaleLabel) {
				f[indexed("questions", i, "options")] = fmt.Sprintf("Cada etiqueta es obligatoria y de hasta %d caracteres.", MaxScaleLabel)
				break
			}
		}
		return options, nil
	}
	return options, q.CorrectOption
}

// CountByType cuenta las preguntas de cada tipo.
func CountByType(questions []Question) (multiple, likert, frequency int) {
	for _, q := range questions {
		switch q.Type {
		case TypeMultipleChoice:
			multiple++
		case TypeLikert:
			likert++
		case TypeFrequency:
			frequency++
		}
	}
	return
}

// CheckPublishable valida que el borrador pueda publicarse: todas las preguntas de
// opción múltiple tienen respuesta correcta y, si es el instrumento CAP oficial,
// su composición es exactamente 8 opción múltiple, 8 Likert y 10 de frecuencia.
func CheckPublishable(questions []Question, isCAP bool) error {
	f := map[string]string{}
	if len(questions) == 0 {
		f["questions"] = "Agrega al menos una pregunta."
	}
	for i, q := range questions {
		if q.Type == TypeMultipleChoice && q.CorrectOption == nil {
			f[indexed("questions", i, "correctOption")] = "Indica la respuesta correcta para poder puntuar conocimientos."
		}
	}
	if isCAP {
		m, l, fr := CountByType(questions)
		if m != CAPMultipleChoice || l != CAPLikert || fr != CAPFrequency {
			f["questions"] = fmt.Sprintf("El instrumento CAP requiere %d de opción múltiple, %d Likert y %d de frecuencia; hay %d, %d y %d.",
				CAPMultipleChoice, CAPLikert, CAPFrequency, m, l, fr)
		}
	}
	if len(f) > 0 {
		return &IncompleteError{Fields: f}
	}
	return nil
}
