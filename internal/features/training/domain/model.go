// Package domain define las reglas de la Academia BPM: cursos versionados,
// calificación en servidor, progreso y videos privados. No conoce HTTP, SQL ni R2.
package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Estados de curso y versión.
const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusArchived  = "ARCHIVED"
)

// Estados de progreso de un usuario.
const (
	ProgressNotStarted = "NOT_STARTED"
	ProgressInProgress = "IN_PROGRESS"
	ProgressCompleted  = "COMPLETED"
)

// Tipos de intento calificado.
const (
	AttemptDiagnostic = "DIAGNOSTIC"
	AttemptFinal      = "FINAL"
)

// Límites de autoría del contrato.
const (
	MaxLessons        = 50
	MaxQuestions      = 100
	MinOptions        = 2
	MaxOptions        = 6
	MaxOptionLength   = 300
	MaxTitleLength    = 200
	MaxContentLength  = 20000
	DefaultPassScore  = 80
	XPPerCorrectFinal = 10
)

var (
	uuidPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	organizationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	codePattern         = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,49}$`)
)

// ValidID indica si v es un UUID textual.
func ValidID(v string) bool { return uuidPattern.MatchString(v) }

// ValidOrganization valida el formato del identificador de organización.
func ValidOrganization(v string) bool { return organizationPattern.MatchString(v) }

// ValidCourseStatus valida el filtro de estado del listado de administración.
func ValidCourseStatus(v string) bool {
	return v == StatusDraft || v == StatusPublished || v == StatusArchived
}

// Actor es el usuario autenticado con el rol obtenido de la membresía (o SUPER_ADMIN de plataforma).
type Actor struct{ ID, Role string }

// CanLearn indica si el rol puede consultar cursos publicados y estudiar.
func (a Actor) CanLearn() bool {
	return a.Role == "ADMIN" || a.Role == "CHEF" || a.Role == "WORKER" || a.Role == "SUPER_ADMIN"
}

// CanPersist indica si el actor tiene una membresía real. Progreso, intentos y
// constancias referencian la membresía; SUPER_ADMIN de plataforma no la tiene.
func (a Actor) CanPersist() bool {
	return a.Role == "ADMIN" || a.Role == "CHEF" || a.Role == "WORKER"
}

// CanAuthor indica si el rol puede administrar cursos, borradores y videos.
func (a Actor) CanAuthor() bool { return a.Role == "ADMIN" || a.Role == "SUPER_ADMIN" }

// Course es la cabecera de un curso.
type Course struct {
	ID, Code, Title, Description string
	EstimatedDurationMinutes     *int
	Status                       string
}

// Version identifica una versión de contenido. RowVersion solo aplica al borrador.
type Version struct {
	ID           string
	Number       int
	PassingScore int
	RowVersion   int
	PublishedAt  *time.Time
}

// Lesson es una lección de autoría; VideoMediaID vacío significa sin video.
type Lesson struct {
	ID, Title, Content string
	DisplayOrder       int
	VideoMediaID       string
}

// Video describe el video disponible de una lección publicada.
type Video struct {
	MediaID, MIMEType string
	SizeBytes         int64
}

// LearnLesson es la lección tal como la ve quien aprende.
type LearnLesson struct {
	Lesson
	Video *Video
}

// Question incluye la respuesta correcta; solo se usa en autoría y calificación.
type Question struct {
	ID, LessonID, Text string
	Options            []string
	CorrectOption      int
	Explanation        string
	DisplayOrder       int
}

// PublicQuestion es la pregunta para aprender: nunca contiene respuesta ni explicación.
type PublicQuestion struct {
	ID, LessonID, Text string
	Options            []string
	DisplayOrder       int
}

// Progress es el avance del usuario en un curso.
type Progress struct {
	Status           string
	LastLessonID     string
	BestScore        *int
	ExperiencePoints int
	CompletedAt      *time.Time
}

// Certificate es una constancia interna verificable.
type Certificate struct {
	ID, VerificationCode string
	IssuedAt             time.Time
}

// CourseView es la lectura para aprender de la versión publicada.
type CourseView struct {
	Course      Course
	Version     Version
	Lessons     []LearnLesson
	Questions   []PublicQuestion
	Progress    Progress
	Certificate *Certificate
}

// Draft es la versión editable con respuestas y explicaciones.
type Draft struct {
	Course    Course
	Version   Version
	Lessons   []Lesson
	Questions []Question
}

// CourseSummary es una fila del listado de administración.
type CourseSummary struct {
	ID, Code, Title, Status string
	PublishedVersionNumber  *int
	HasDraft                bool
}

// NewCourse son los datos para crear un curso.
type NewCourse struct {
	Code, Title, Description string
	EstimatedDurationMinutes *int
}

// Normalize valida y normaliza los datos de un curso nuevo.
func (c NewCourse) Normalize() (NewCourse, error) {
	fields := map[string]string{}
	c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
	c.Title = strings.TrimSpace(c.Title)
	c.Description = strings.TrimSpace(c.Description)
	if !codePattern.MatchString(c.Code) {
		fields["code"] = "Usa de 1 a 50 letras, números, guiones o guiones bajos."
	}
	if !validText(c.Title, 2, MaxTitleLength, false) {
		fields["title"] = "El título debe tener entre 2 y 200 caracteres."
	}
	if !validText(c.Description, 0, 2000, true) {
		fields["description"] = "La descripción admite hasta 2000 caracteres."
	}
	if c.EstimatedDurationMinutes != nil && (*c.EstimatedDurationMinutes < 1 || *c.EstimatedDurationMinutes > 1440) {
		fields["estimatedDurationMinutes"] = "La duración debe estar entre 1 y 1440 minutos."
	}
	return c, invalid(fields)
}

// validText comprueba UTF-8, longitud en caracteres y ausencia de controles.
// Los saltos de línea y tabulaciones solo se admiten cuando multiline es true.
func validText(v string, min, max int, multiline bool) bool {
	n := utf8.RuneCountInString(v)
	if !utf8.ValidString(v) || n < min || n > max {
		return false
	}
	return strings.IndexFunc(v, func(r rune) bool {
		if multiline && (r == '\n' || r == '\t' || r == '\r') {
			return false
		}
		return unicode.IsControl(r)
	}) < 0
}
