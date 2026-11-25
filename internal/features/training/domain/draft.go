package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// DraftInput es el contenido que el administrador envía para reemplazar el borrador.
// Los IDs de lecciones y preguntas los genera el cliente (UUID).
type DraftInput struct {
	// VersionID es opcional; si se envía debe coincidir con el borrador vigente.
	VersionID    string
	PassingScore int
	RowVersion   int
	Lessons      []Lesson
	Questions    []Question
}

// NormalizeDraft valida los límites del contrato y devuelve el contenido con
// identificadores en minúsculas y órdenes consecutivos 1..n. Reúne todos los
// errores por campo (por ejemplo, "questions[2].options").
func NormalizeDraft(in DraftInput) (DraftInput, error) {
	fields := map[string]string{}
	in.VersionID = strings.ToLower(in.VersionID)
	if in.VersionID != "" && !ValidID(in.VersionID) {
		fields["version.id"] = "El identificador de la versión no es válido."
	}
	if in.PassingScore < 1 || in.PassingScore > 100 {
		fields["version.passingScore"] = "El puntaje de aprobación debe estar entre 1 y 100."
	}
	if in.RowVersion < 1 {
		fields["version.rowVersion"] = "Falta la versión del borrador; recárgalo antes de guardar."
	}
	// Una lista ausente (nil) no equivale a una lista vacía: evita borrar el borrador por omisión.
	if in.Lessons == nil {
		fields["lessons"] = "Envía la lista completa de lecciones (puede estar vacía)."
	}
	if in.Questions == nil {
		fields["questions"] = "Envía la lista completa de preguntas (puede estar vacía)."
	}
	if len(in.Lessons) > MaxLessons {
		fields["lessons"] = fmt.Sprintf("Un curso admite hasta %d lecciones.", MaxLessons)
	}
	if len(in.Questions) > MaxQuestions {
		fields["questions"] = fmt.Sprintf("Un curso admite hasta %d preguntas.", MaxQuestions)
	}
	if len(in.Lessons) > MaxLessons || len(in.Questions) > MaxQuestions {
		return in, invalid(fields)
	}

	lessons := make([]Lesson, len(in.Lessons))
	copy(lessons, in.Lessons)
	lessonIDs := map[string]bool{}
	orders := map[int]bool{}
	for i := range lessons {
		l := &lessons[i]
		l.ID = strings.ToLower(l.ID)
		l.Title = strings.TrimSpace(l.Title)
		l.VideoMediaID = strings.ToLower(l.VideoMediaID)
		switch {
		case !ValidID(l.ID):
			fields[indexed("lessons", i, "id")] = "Cada lección necesita un identificador UUID."
		case lessonIDs[l.ID]:
			fields[indexed("lessons", i, "id")] = "El identificador de la lección está repetido."
		}
		lessonIDs[l.ID] = true
		if !validText(l.Title, 1, MaxTitleLength, false) {
			fields[indexed("lessons", i, "title")] = "El título debe tener entre 1 y 200 caracteres."
		}
		if !validText(l.Content, 0, MaxContentLength, true) {
			fields[indexed("lessons", i, "content")] = "El contenido admite hasta 20000 caracteres."
		}
		if l.VideoMediaID != "" && !ValidID(l.VideoMediaID) {
			fields[indexed("lessons", i, "videoMediaId")] = "El identificador del video no es válido."
		}
		if l.PlatformVideoID != "" && (!ValidID(l.PlatformVideoID) || l.VideoMediaID != "" || l.ExternalVideo != nil) {
			fields[indexed("lessons", i, "videoMediaId")] = "Una lección admite un solo video."
		}
		if l.ExternalVideo != nil {
			if l.VideoMediaID != "" {
				fields[indexed("lessons", i, "externalVideo")] = "Una lección admite video privado o externo, no ambos."
			} else if l.ExternalVideo.Provider != ProviderYouTube || !ValidYouTubeID(l.ExternalVideo.VideoID) {
				fields[indexed("lessons", i, "externalVideo")] = "Usa un video de YouTube válido."
			}
		}
		if l.DisplayOrder < 1 || orders[l.DisplayOrder] {
			fields[indexed("lessons", i, "displayOrder")] = "El orden debe ser positivo y no repetirse."
		}
		orders[l.DisplayOrder] = true
		points, msg := NormalizeKeyPoints(l.KeyPoints)
		if msg != "" {
			fields[indexed("lessons", i, "keyPoints")] = msg
		}
		l.KeyPoints = points
		if l.Captions != nil {
			captions, msg := NormalizeCaptions(*l.Captions)
			if msg != "" {
				fields[indexed("lessons", i, "captionsVtt")] = msg
			}
			l.Captions = &captions
		}
	}

	questions := make([]Question, len(in.Questions))
	copy(questions, in.Questions)
	questionIDs := map[string]bool{}
	orders = map[int]bool{}
	for i := range questions {
		q := &questions[i]
		q.ID = strings.ToLower(q.ID)
		q.LessonID = strings.ToLower(q.LessonID)
		q.Text = strings.TrimSpace(q.Text)
		q.Explanation = strings.TrimSpace(q.Explanation)
		switch {
		case !ValidID(q.ID):
			fields[indexed("questions", i, "id")] = "Cada pregunta necesita un identificador UUID."
		case questionIDs[q.ID]:
			fields[indexed("questions", i, "id")] = "El identificador de la pregunta está repetido."
		}
		questionIDs[q.ID] = true
		if q.LessonID != "" && !lessonIDs[q.LessonID] {
			fields[indexed("questions", i, "lessonId")] = "La lección debe existir en este mismo borrador."
		}
		if !validText(q.Text, 1, 1000, true) {
			fields[indexed("questions", i, "questionText")] = "El enunciado debe tener entre 1 y 1000 caracteres."
		}
		if !validText(q.Explanation, 0, 2000, true) {
			fields[indexed("questions", i, "explanation")] = "La explicación admite hasta 2000 caracteres."
		}
		q.Options = CleanOptions(q.Options)
		if msg := validateOptions(q.Options); msg != "" {
			fields[indexed("questions", i, "options")] = msg
		} else if q.CorrectOption < 0 || q.CorrectOption >= len(q.Options) {
			fields[indexed("questions", i, "correctOption")] = "La respuesta correcta debe ser una de las opciones."
		}
		if q.DisplayOrder < 1 || orders[q.DisplayOrder] {
			fields[indexed("questions", i, "displayOrder")] = "El orden debe ser positivo y no repetirse."
		}
		orders[q.DisplayOrder] = true
	}
	if len(fields) > 0 {
		return in, invalid(fields)
	}

	sort.SliceStable(lessons, func(a, b int) bool { return lessons[a].DisplayOrder < lessons[b].DisplayOrder })
	for i := range lessons {
		lessons[i].DisplayOrder = i + 1
	}
	sort.SliceStable(questions, func(a, b int) bool { return questions[a].DisplayOrder < questions[b].DisplayOrder })
	for i := range questions {
		questions[i].DisplayOrder = i + 1
	}
	in.Lessons, in.Questions = lessons, questions
	return in, nil
}

// NormalizeKeyPoints valida "Lo esencial" de una lección: hasta 5 puntos, cada uno de 1 a
// 200 caracteres en una sola línea, sin repetirse (sin distinguir mayúsculas). Devuelve los
// puntos sin espacios sobrantes; nil (no enviado) se conserva como nil y un arreglo vacío como vacío.
func NormalizeKeyPoints(points []string) ([]string, string) {
	if points == nil {
		return nil, ""
	}
	if len(points) > MaxKeyPoints {
		return points, fmt.Sprintf("Lo esencial admite hasta %d puntos.", MaxKeyPoints)
	}
	out := make([]string, len(points))
	seen := map[string]bool{}
	for i, point := range points {
		trimmed := strings.TrimSpace(point)
		if !validText(trimmed, 1, MaxKeyPointLength, false) {
			return points, fmt.Sprintf("Cada punto debe tener entre 1 y %d caracteres en una sola línea.", MaxKeyPointLength)
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			return points, "Los puntos de lo esencial no pueden repetirse."
		}
		seen[key] = true
		out[i] = trimmed
	}
	return out, ""
}

// NormalizeCaptions valida los subtítulos WebVTT de una lección (RF-45): vacío (sin subtítulos) o
// un documento que empieza con "WEBVTT", tiene al menos un tiempo "inicio --> fin" y no supera
// MaxCaptionsLength caracteres. Quita la marca BOM, unifica los saltos de línea y los espacios
// del final. Devuelve el texto normalizado y un mensaje para la persona si no es válido.
func NormalizeCaptions(raw string) (string, string) {
	text := strings.TrimPrefix(raw, "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	if utf8.RuneCountInString(text) > MaxCaptionsLength {
		return raw, fmt.Sprintf("Los subtítulos admiten hasta %d caracteres.", MaxCaptionsLength)
	}
	if !utf8.ValidString(text) || !strings.HasPrefix(text, "WEBVTT") {
		return raw, "Usa un archivo de subtítulos WebVTT: debe empezar con WEBVTT."
	}
	if !captionTiming.MatchString(text) {
		return raw, "Los subtítulos no tienen tiempos válidos (por ejemplo 00:00:01.000 --> 00:00:04.000)."
	}
	return text + "\n", ""
}

// captionTiming reconoce una línea de tiempos WebVTT: [hh:]mm:ss.ttt --> [hh:]mm:ss.ttt.
var captionTiming = regexp.MustCompile(`(?m)^(\d{2,}:)?\d{2}:\d{2}\.\d{3}\s+-->\s+(\d{2,}:)?\d{2}:\d{2}\.\d{3}`)

// validateOptions exige de 2 a 6 opciones no vacías, de hasta 300 caracteres y sin repetirse.
func validateOptions(options []string) string {
	if len(options) < MinOptions || len(options) > MaxOptions {
		return "Cada pregunta necesita entre 2 y 6 opciones."
	}
	seen := map[string]bool{}
	for _, option := range options {
		trimmed := strings.TrimSpace(option)
		if !validText(trimmed, 1, MaxOptionLength, false) {
			return "Cada opción debe tener entre 1 y 300 caracteres."
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			return "Las opciones de una pregunta no pueden repetirse."
		}
		seen[key] = true
	}
	return ""
}

// CleanOptions devuelve las opciones sin espacios sobrantes.
func CleanOptions(options []string) []string {
	out := make([]string, len(options))
	for i, o := range options {
		out[i] = strings.TrimSpace(o)
	}
	return out
}

// ValidateForPublish exige al menos una lección, una pregunta y videos AVAILABLE.
// videoStatus asocia cada media_id referenciado con su estado actual; un video
// ausente del mapa (de otra organización o inexistente) cuenta como no disponible.
func (d Draft) ValidateForPublish(videoStatus map[string]string) error {
	fields := map[string]string{}
	if len(d.Lessons) < 1 {
		fields["lessons"] = "Agrega al menos una lección antes de publicar."
	}
	if len(d.Questions) < 1 {
		fields["questions"] = "Agrega al menos una pregunta antes de publicar."
	}
	for i, l := range d.Lessons {
		if l.VideoMediaID != "" && videoStatus[l.VideoMediaID] != "AVAILABLE" {
			fields[indexed("lessons", i, "videoMediaId")] = "El video aún no está disponible; confirma su carga."
		}
	}
	if len(fields) > 0 {
		return &IncompleteError{Fields: fields}
	}
	return nil
}
