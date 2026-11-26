package domain

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	idItem  = "40000000-0000-0000-0000-00000000000a"
	idSite  = "50000000-0000-0000-0000-00000000000b"
	idAsign = "60000000-0000-0000-0000-00000000000c"
)

func TestBuildPushPorTipo(t *testing.T) {
	due := time.Date(2026, 12, 3, 3, 0, 0, 0, time.UTC) // 02/12 22:00 en Lima
	for _, tc := range []struct {
		name               string
		facts              PushFacts
		title, body, route string
		sourceType, entity string
	}{
		{"temperatura ADMIN", PushFacts{AlertType: "TEMPERATURE_DANGER", SourceType: "RECEIPT_ITEM", SourceID: idItem, LocationID: idSite, SiteName: "Sede Centro", Subject: "Pollo", Category: "Carnes", Temperature: "12.00", RecipientRole: "ADMIN"},
			"Temperatura fuera de rango · Sede Centro", "Pollo (Carnes): 12 °C", "empresa/operacion", "RECEIPT_ITEM", idItem},
		{"temperatura CHEF decimal", PushFacts{AlertType: "TEMPERATURE_DANGER", SourceType: "RECEIPT_ITEM", SourceID: idItem, SiteName: "Sede Centro", Subject: "Leche", Temperature: "4.50", RecipientRole: "CHEF"},
			"Temperatura fuera de rango · Sede Centro", "Leche: 4.5 °C", "control/operacion", "RECEIPT_ITEM", idItem},
		{"temperatura sin datos", PushFacts{AlertType: "TEMPERATURE_DANGER", SourceType: "RECEIPT_ITEM", SourceID: idItem, Temperature: "nan"},
			"Temperatura fuera de rango", "Revisa la medición de la recepción.", "control/operacion", "RECEIPT_ITEM", idItem},
		{"control pendiente", PushFacts{AlertType: "CHECKLIST_MISSING", SourceType: "CHECKLIST_ASSIGNMENT", SourceID: idAsign, LocationID: idSite, SiteName: "Sede Centro", Subject: "Apertura de cocina"},
			"Control pendiente: Apertura de cocina", "Aún no se registra el control de hoy · Sede Centro", "control/programacion/" + idAsign, "CHECKLIST_ASSIGNMENT", idAsign},
		{"control sin plantilla", PushFacts{AlertType: "CHECKLIST_MISSING", SourceType: "CHECKLIST_ASSIGNMENT", SourceID: "no-uuid"},
			"Control pendiente", "Aún no se registra el control de hoy", "avisos", "CHECKLIST_ASSIGNMENT", ""},
		{"empresa inactiva", PushFacts{AlertType: "ORGANIZATION_INACTIVE", SourceType: "ORGANIZATION", SourceID: "org", InactivityDays: "7"},
			"Sin actividad reciente en BPM", "No hay actividad desde hace 7 días. Revisa la operación.", "empresa", "ORGANIZATION", ""},
		{"empresa inactiva 1 día", PushFacts{AlertType: "ORGANIZATION_INACTIVE", InactivityDays: "1"},
			"Sin actividad reciente en BPM", "No hay actividad desde hace 1 día. Revisa la operación.", "empresa", "", ""},
		{"capacitación con vencimiento", PushFacts{AlertType: "TRAINING_ASSIGNED", SourceType: "TRAINING_ASSIGNMENT", SourceID: idAsign, SiteName: "Sede Centro", Subject: "Higiene personal", DueAt: &due, Zone: "America/Lima"},
			"Capacitación asignada: Higiene personal", "Sede Centro · Vence el 02/12/2026. Complétala en la Academia.", "aprender", "TRAINING_ASSIGNMENT", idAsign},
		{"capacitación sin sede ni plazo", PushFacts{AlertType: "TRAINING_ASSIGNED", SourceType: "TRAINING_ASSIGNMENT", SourceID: idAsign, Subject: "BPM básico"},
			"Capacitación asignada: BPM básico", "Complétala en la Academia.", "aprender", "TRAINING_ASSIGNMENT", idAsign},
		{"incidencia asignada WORKER", PushFacts{AlertType: "INCIDENT_ASSIGNED", SourceType: "INCIDENT", SourceID: idItem, LocationID: idSite, SiteName: "Sede Centro", Subject: "Superficie sucia", DueAt: &due, Zone: "America/Lima", RecipientRole: "WORKER"},
			"Te asignaron una incidencia: Superficie sucia", "Sede Centro · Plazo 02/12/2026 22:00", "control/incidencias", "INCIDENT", idItem},
		{"incidencia asignada CHEF", PushFacts{AlertType: "INCIDENT_ASSIGNED", SourceType: "INCIDENT", SourceID: idItem, SiteName: "Sede Centro", Subject: "Plaga", DueAt: &due, Zone: "America/Lima", RecipientRole: "CHEF"},
			"Te asignaron una incidencia: Plaga", "Sede Centro · Plazo 02/12/2026 22:00", "control/incidencias", "INCIDENT", idItem},
		{"incidencia asignada ADMIN", PushFacts{AlertType: "INCIDENT_ASSIGNED", SourceType: "INCIDENT", SourceID: idItem, SiteName: "Sede Centro", Subject: "Plaga", DueAt: &due, Zone: "America/Lima", RecipientRole: "ADMIN"},
			"Te asignaron una incidencia: Plaga", "Sede Centro · Plazo 02/12/2026 22:00", "empresa/incidencias", "INCIDENT", idItem},
		{"incidencia asignada sin sede ni plazo", PushFacts{AlertType: "INCIDENT_ASSIGNED", SourceType: "INCIDENT", SourceID: idItem, RecipientRole: "WORKER"},
			"Te asignaron una incidencia", "Revisa la acción correctiva asignada.", "control/incidencias", "INCIDENT", idItem},
		{"incidencia asignada sin id válido", PushFacts{AlertType: "INCIDENT_ASSIGNED", SourceType: "INCIDENT", SourceID: "no-uuid", SiteName: "Sede Centro", Subject: "Plaga"},
			"Te asignaron una incidencia: Plaga", "Sede Centro · Revisa la acción correctiva asignada.", "avisos", "INCIDENT", ""},
		{"tipo desconocido", PushFacts{AlertType: "OTRO_TIPO", SourceType: "INCIDENT", SourceID: idItem, SiteName: "Sede Centro"},
			GenericPushTitle, GenericPushBody, "avisos", "INCIDENT", idItem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildPush(tc.facts)
			if got.Title != tc.title || got.Body != tc.body || got.Route != tc.route || got.SourceType != tc.sourceType || got.EntityID != tc.entity || got.Type != tc.facts.AlertType {
				t.Fatalf("obtenido %+v", got)
			}
		})
	}
}

func TestBuildPushSedeEnDatos(t *testing.T) {
	got := BuildPush(PushFacts{AlertType: "TEMPERATURE_DANGER", LocationID: strings.ToUpper(idSite)})
	if got.SiteID != idSite {
		t.Fatal("siteId no normalizado", got.SiteID)
	}
	if BuildPush(PushFacts{AlertType: "TEMPERATURE_DANGER", LocationID: "x/../y"}).SiteID != "" {
		t.Fatal("siteId inválido aceptado")
	}
}

func TestBuildPushLongitudYRunas(t *testing.T) {
	long := strings.Repeat("ñ", 300)
	for _, f := range []PushFacts{
		{AlertType: "TEMPERATURE_DANGER", SiteName: long, Subject: long, Category: long, Temperature: "1"},
		{AlertType: "CHECKLIST_MISSING", SiteName: long, Subject: long},
		{AlertType: "TRAINING_ASSIGNED", SiteName: long, Subject: long},
		{AlertType: "INCIDENT_ASSIGNED", SiteName: long, Subject: long},
	} {
		got := BuildPush(f)
		if utf8.RuneCountInString(got.Title) > PushTitleMax || utf8.RuneCountInString(got.Body) > PushBodyMax || !utf8.ValidString(got.Title+got.Body) {
			t.Fatalf("fuera de límite: %d/%d", utf8.RuneCountInString(got.Title), utf8.RuneCountInString(got.Body))
		}
		if !strings.HasSuffix(got.Title, "…") && f.AlertType != "TEMPERATURE_DANGER" {
			t.Fatal("sin elipsis", got.Title)
		}
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"hola", 4, "hola"},
		{"hola mundo", 5, "hola…"},
		{"hola mundo", 6, "hola…"}, // el espacio final se recorta antes de la elipsis
		{"añoñoñoñ", 4, "año…"},
		{"日本語のテキスト", 4, "日本語…"},
		{"x", 0, "x"},
	} {
		if got := Truncate(tc.in, tc.max); got != tc.want {
			t.Fatalf("%q/%d: %q != %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestBuildPushLimpiaControlesYNoFiltraPersonas(t *testing.T) {
	got := BuildPush(PushFacts{AlertType: "CHECKLIST_MISSING", SiteName: "Sede\r\nCentro\x00", Subject: "  Apertura \t de\ncocina ", RecipientRole: "ADMIN"})
	if got.Title != "Control pendiente: Apertura de cocina" || got.Body != "Aún no se registra el control de hoy · Sede Centro" {
		t.Fatalf("texto sin limpiar: %+v", got)
	}
	// El destinatario no forma parte de PushFacts: ningún campo de nombre, correo o teléfono llega al texto.
	todo := got.Title + got.Body + got.Route + got.Type + got.SourceType + got.EntityID + got.SiteID
	for _, privado := range []string{"@", "ADMIN", "token", "http"} {
		if strings.Contains(todo, privado) {
			t.Fatalf("dato privado %q en el aviso: %s", privado, todo)
		}
	}
}
