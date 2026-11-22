// Package domain define el agente normativo (RF-46/47): documentos de normativa cargados por el
// administrador general y respuestas que solo citan esos documentos.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxTitleLength    = 200
	MaxSourceLength   = 300
	MaxContentLength  = 400_000
	MaxQuestionLength = 1000
	// MaxActiveCharacters acota la normativa activa que se envía en cada consulta (~400 mil tokens).
	MaxActiveCharacters = 1_500_000
	// QuestionsPerHour limita las consultas por persona para contener el costo.
	QuestionsPerHour = 20

	StatusActive   = "ACTIVE"
	StatusArchived = "ARCHIVED"
)

var (
	ErrDenied      = errors.New("normative: denied")
	ErrNotFound    = errors.New("normative: document not found")
	ErrDisabled    = errors.New("normative: assistant disabled")
	ErrNoDocuments = errors.New("normative: no active documents")
	ErrRateLimited = errors.New("normative: too many questions")
	ErrTooLarge    = errors.New("normative: active documents exceed the limit")
	ErrUnavailable = errors.New("normative: model unavailable")
)

// ValidationError agrupa campos inválidos (HTTP 422).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid normative input" }

// Document es una norma cargada por el administrador general.
type Document struct {
	ID, Title, Source, Content, Status string
	Characters                         int
	CreatedAt, UpdatedAt               time.Time
}

// DocumentInput es lo que el administrador general envía al crear o editar.
type DocumentInput struct{ Title, Source, Content string }

// Citation es un fragmento literal de un documento que respalda la respuesta.
type Citation struct {
	DocumentID    string `json:"documentId"`
	DocumentTitle string `json:"documentTitle"`
	CitedText     string `json:"citedText"`
}

// Answer es la respuesta del agente. Declined indica que la normativa cargada no cubre la pregunta
// o que el modelo no pudo responder; en ese caso no hay citas.
type Answer struct {
	Text      string
	Citations []Citation
	Declined  bool
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidID acepta UUID en minúsculas.
func ValidID(id string) bool { return uuidPattern.MatchString(id) }

// NormalizeDocument recorta espacios, unifica saltos de línea y valida longitudes.
func NormalizeDocument(in DocumentInput) (DocumentInput, error) {
	out := DocumentInput{
		Title:   strings.TrimSpace(in.Title),
		Source:  strings.TrimSpace(in.Source),
		Content: strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(in.Content, "\ufeff"), "\r\n", "\n"), "\r", "\n")),
	}
	fields := map[string]string{}
	if n := utf8.RuneCountInString(out.Title); n < 1 || n > MaxTitleLength || strings.ContainsAny(out.Title, "\n\r") {
		fields["title"] = "El título es obligatorio, de una línea y de hasta 200 caracteres."
	}
	if utf8.RuneCountInString(out.Source) > MaxSourceLength || strings.ContainsAny(out.Source, "\n\r") {
		fields["source"] = "La referencia admite una línea de hasta 300 caracteres."
	}
	if n := utf8.RuneCountInString(out.Content); n < 1 || n > MaxContentLength {
		fields["content"] = "Pega el texto de la norma (hasta 400 000 caracteres)."
	}
	if !utf8.ValidString(out.Title + out.Source + out.Content) {
		fields["content"] = "El texto tiene caracteres no válidos."
	}
	if len(fields) > 0 {
		return in, &ValidationError{Fields: fields}
	}
	return out, nil
}

// NormalizeQuestion recorta la pregunta y valida su longitud.
func NormalizeQuestion(q string) (string, error) {
	q = strings.TrimSpace(q)
	if n := utf8.RuneCountInString(q); n < 3 || n > MaxQuestionLength || !utf8.ValidString(q) {
		return q, &ValidationError{Fields: map[string]string{"question": "Escribe tu pregunta (de 3 a 1000 caracteres)."}}
	}
	return q, nil
}
