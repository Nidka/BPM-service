package domain

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBuildText(t *testing.T) {
	at := time.Date(2026, 11, 10, 20, 35, 0, 0, time.UTC)
	due := time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC) // 30/11 22:00 en Lima
	cases := []struct {
		name          string
		facts         TextFacts
		title, detail string
	}{
		{"temperatura completa", TextFacts{Type: "TEMPERATURE_DANGER", Site: "Sede Centro", Subject: "Pollo", Category: "Carnes", Temperature: "12.00", At: &at, Zone: "America/Lima"},
			"Temperatura fuera de rango · Sede Centro", "Pollo (Carnes): 12 °C · 15:35"},
		{"temperatura decimal sin categoría", TextFacts{Type: "TEMPERATURE_DANGER", Site: "Sede", Subject: "Leche", Temperature: "4.50", At: &at, Zone: "UTC"},
			"Temperatura fuera de rango · Sede", "Leche: 4.5 °C · 20:35"},
		{"temperatura sin datos", TextFacts{Type: "TEMPERATURE_DANGER", Temperature: "nan"},
			"Temperatura fuera de rango", "Revisa la medición y el criterio configurado de la recepción."},
		{"zona inválida usa Lima", TextFacts{Type: "TEMPERATURE_DANGER", Subject: "Pollo", Temperature: "8", At: &at, Zone: "No/Existe"},
			"Temperatura fuera de rango", "Pollo: 8 °C · 15:35"},
		{"control pendiente", TextFacts{Type: "CHECKLIST_MISSING", Site: "Sede Centro", Subject: "Apertura de cocina", AfterLocalTime: "10:00"},
			"Control pendiente: Apertura de cocina · Sede Centro", "Aún no se registra el control de hoy. Hora configurada: 10:00."},
		{"control sin plantilla", TextFacts{Type: "CHECKLIST_MISSING"},
			"Control pendiente", "Aún no se registra el control de hoy."},
		{"capacitación con vencimiento", TextFacts{Type: "TRAINING_ASSIGNED", Subject: "Inocuidad", DueAt: &due, Zone: "America/Lima"},
			"Capacitación asignada: Inocuidad", "Vence 30/11/2026"},
		{"capacitación sin vencimiento", TextFacts{Type: "TRAINING_ASSIGNED", Subject: "Inocuidad"},
			"Capacitación asignada: Inocuidad", "Complétala en la Academia."},
		{"inactividad usa texto genérico", TextFacts{Type: "ORGANIZATION_INACTIVE"},
			"Empresa sin actividad reciente", "Revisa la actividad operativa de la empresa."},
		{"limpia saltos de línea", TextFacts{Type: "CHECKLIST_MISSING", Site: "Sede\nA", Subject: "  Control\t1 "},
			"Control pendiente: Control 1 · Sede A", "Aún no se registra el control de hoy."},
	}
	for _, c := range cases {
		title, message := BuildText(c.facts)
		if title != c.title || message != c.detail {
			t.Errorf("%s: %q / %q", c.name, title, message)
		}
	}
}

func TestBuildTextTruncatesTitle(t *testing.T) {
	title, _ := BuildText(TextFacts{Type: "TRAINING_ASSIGNED", Subject: strings.Repeat("ñ", 400)})
	if utf8.RuneCountInString(title) != alertTitleMax || !strings.HasSuffix(title, "…") {
		t.Fatalf("título sin recortar: %d", utf8.RuneCountInString(title))
	}
}
