package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeCaptionsAcceptsWebVTT(t *testing.T) {
	raw := "\ufeffWEBVTT\r\n\r\n00:00:01.000 --> 00:00:04.000\r\nLávate las manos antes de empezar.\r\n"
	got, msg := NormalizeCaptions(raw)
	if msg != "" {
		t.Fatalf("mensaje inesperado: %q", msg)
	}
	if strings.Contains(got, "\r") || strings.HasPrefix(got, "\ufeff") || !strings.HasPrefix(got, "WEBVTT") {
		t.Fatalf("no normalizó BOM ni saltos de línea: %q", got)
	}
}

func TestNormalizeCaptionsEmptyMeansNone(t *testing.T) {
	if got, msg := NormalizeCaptions("  \n "); got != "" || msg != "" {
		t.Fatalf("vacío debe quitar los subtítulos: %q %q", got, msg)
	}
}

func TestNormalizeCaptionsRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"sin cabecera":    "00:00:01.000 --> 00:00:02.000\nHola",
		"sin tiempos":     "WEBVTT\n\nHola",
		"formato SRT":     "WEBVTT\n\n1\n00:00:01,000 --> 00:00:02,000\nHola",
		"demasiado largo": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n" + strings.Repeat("a", MaxCaptionsLength),
	}
	for name, raw := range cases {
		if _, msg := NormalizeCaptions(raw); msg == "" {
			t.Errorf("%s: debía rechazarse", name)
		}
	}
}

func TestNormalizeDraftReportsCaptionsField(t *testing.T) {
	bad := "texto suelto"
	in := DraftInput{PassingScore: 80, RowVersion: 1, Questions: []Question{},
		Lessons: []Lesson{{ID: "11111111-1111-4111-8111-111111111111", Title: "Lavado", Content: "Contenido", DisplayOrder: 1, Captions: &bad}}}
	_, err := NormalizeDraft(in)
	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Fields["lessons[0].captionsVtt"] == "" {
		t.Fatalf("debía marcar lessons[0].captionsVtt, obtuvo %v", err)
	}
}
