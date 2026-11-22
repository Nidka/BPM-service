// Package domain define el buscador de normativa (RF-46/47): documentos cargados por el administrador
// general, divididos en fragmentos que cualquier persona activa puede buscar. Sin servicios externos.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxTitleLength   = 200
	MaxSourceLength  = 300
	MaxContentLength = 400_000
	MinQueryLength   = 2
	MaxQueryLength   = 200
	// MaxFragmentLength acota cada fragmento buscable (un párrafo largo se divide por oraciones).
	MaxFragmentLength = 1500
	// MaxResults limita los fragmentos que devuelve una búsqueda.
	MaxResults = 20

	StatusActive   = "ACTIVE"
	StatusArchived = "ARCHIVED"
)

var (
	ErrDenied   = errors.New("normative: denied")
	ErrNotFound = errors.New("normative: document not found")
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

// SearchResult es un fragmento de la normativa activa que coincide con la búsqueda.
type SearchResult struct {
	DocumentID, DocumentTitle, DocumentSource, Text string
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidID acepta UUID en minúsculas.
func ValidID(id string) bool { return uuidPattern.MatchString(id) }

// NormalizeDocument recorta espacios, unifica saltos de línea y valida longitudes.
func NormalizeDocument(in DocumentInput) (DocumentInput, error) {
	content := strings.TrimPrefix(in.Content, "\ufeff")
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	out := DocumentInput{Title: strings.TrimSpace(in.Title), Source: strings.TrimSpace(in.Source), Content: strings.TrimSpace(content)}
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

// NormalizeQuery recorta la búsqueda y valida su longitud.
func NormalizeQuery(q string) (string, error) {
	q = strings.Join(strings.Fields(q), " ")
	if n := utf8.RuneCountInString(q); n < MinQueryLength || n > MaxQueryLength || !utf8.ValidString(q) {
		return q, &ValidationError{Fields: map[string]string{"q": "Escribe lo que buscas (de 2 a 200 caracteres)."}}
	}
	return q, nil
}

var sentenceEnd = regexp.MustCompile(`[.;:!?]\s+`)

// Fragments divide el texto de una norma en párrafos buscables: separa por líneas en blanco, une líneas
// sueltas del mismo párrafo y parte por oraciones lo que supere MaxFragmentLength.
func Fragments(content string) []string {
	out := []string{}
	for _, block := range regexp.MustCompile(`\n\s*\n`).Split(content, -1) {
		paragraph := strings.Join(strings.Fields(block), " ")
		if paragraph == "" {
			continue
		}
		for utf8.RuneCountInString(paragraph) > MaxFragmentLength {
			cut := splitPoint(paragraph)
			out = append(out, strings.TrimSpace(paragraph[:cut]))
			paragraph = strings.TrimSpace(paragraph[cut:])
		}
		if paragraph != "" {
			out = append(out, paragraph)
		}
	}
	return out
}

// splitPoint devuelve el índice en bytes donde cortar: el último fin de oración antes del máximo o, si no
// hay, el último espacio; nunca parte un carácter.
func splitPoint(text string) int {
	limit := 0
	for i := range text {
		if utf8.RuneCountInString(text[:i]) >= MaxFragmentLength {
			break
		}
		limit = i
	}
	window := text[:limit]
	if loc := sentenceEnd.FindAllStringIndex(window, -1); len(loc) > 0 && loc[len(loc)-1][1] > limit/2 {
		return loc[len(loc)-1][1]
	}
	if i := strings.LastIndex(window, " "); i > limit/2 {
		return i + 1
	}
	return limit
}
