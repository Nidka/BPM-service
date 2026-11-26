package domain

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Límites de presentación del aviso en la bandeja (la columna title es VARCHAR(160)).
const (
	alertTitleMax   = 160
	alertMessageMax = 500
	// defaultZone se usa cuando la sede no tiene una zona IANA válida (coherente con REPORTS.md).
	defaultZone = "America/Lima"
)

// TextFacts son datos de negocio ya resueltos por el adaptador para redactar un aviso nuevo. Nunca contiene datos
// personales de quien recibe ni de quien ejecutó la operación: solo sede, plantilla/producto/curso y valores medidos.
type TextFacts struct {
	Type string
	// Site es el nombre de la sede; Subject la plantilla, el producto o el curso según el tipo.
	Site, Subject, Category string
	// Temperature llega como texto numérico de PostgreSQL (por ejemplo "12.00").
	Temperature string
	// At es el instante local mostrado (recepción) y DueAt el vencimiento (capacitación); Zone es la zona IANA de la sede.
	At, DueAt *time.Time
	Zone      string
	// AfterLocalTime es la hora configurada en la regla CHECKLIST_MISSING ("HH:mm").
	AfterLocalTime string
}

// BuildText devuelve título y mensaje específicos para un aviso recién creado. Cada parte ausente se omite y, si no
// se conoce nada, se conserva el texto genérico histórico. Los avisos ya creados no se reescriben.
func BuildText(f TextFacts) (title, message string) {
	site, subject, category := cleanLine(f.Site), cleanLine(f.Subject), cleanLine(f.Category)
	switch f.Type {
	case "TEMPERATURE_DANGER":
		title = withSuffix("Temperatura fuera de rango", " · ", site)
		message = temperatureMessage(subject, category, f.Temperature, f.At, f.Zone)
	case "CHECKLIST_MISSING":
		title = withSuffix("Control pendiente", ": ", subject)
		if site != "" {
			title += " · " + site
		}
		message = "Aún no se registra el control de hoy."
		if hm := strings.TrimSpace(f.AfterLocalTime); hm != "" {
			message += " Hora configurada: " + hm + "."
		}
	case "TRAINING_ASSIGNED":
		title = withSuffix("Capacitación asignada", ": ", subject)
		message = "Complétala en la Academia."
		if f.DueAt != nil {
			message = "Vence " + localTime(*f.DueAt, f.Zone).Format("02/01/2006")
		}
	default:
		title, message = GenericText(f.Type)
	}
	return truncate(title, alertTitleMax), truncate(message, alertMessageMax)
}

// GenericText es el texto de respaldo (el histórico) por tipo de regla.
func GenericText(kind string) (string, string) {
	switch kind {
	case "TEMPERATURE_DANGER":
		return "Temperatura dentro del intervalo de alerta", "Revisa la medición y el criterio configurado de la recepción."
	case "CHECKLIST_MISSING":
		return "Control diario pendiente", "Revisa el control diario pendiente de la sede."
	case "ORGANIZATION_INACTIVE":
		return "Empresa sin actividad reciente", "Revisa la actividad operativa de la empresa."
	default:
		return "Capacitación asignada", "Revisa la capacitación que tienes asignada."
	}
}

// temperatureMessage compone «Pollo (Carnes): 12 °C · 14:35»; omite lo que no se conoce.
func temperatureMessage(product, category, temperature string, at *time.Time, zone string) string {
	name := product
	if name != "" && category != "" {
		name += " (" + category + ")"
	}
	value := formatTemperature(temperature)
	var out string
	switch {
	case name != "" && value != "":
		out = name + ": " + value + " °C"
	case value != "":
		out = "Medición registrada: " + value + " °C"
	case name != "":
		out = name + ": revisa la medición de la recepción."
	default:
		out = "Revisa la medición y el criterio configurado de la recepción."
	}
	if at != nil && value != "" {
		out += " · " + localTime(*at, zone).Format("15:04")
	}
	return out
}

func localTime(t time.Time, zone string) time.Time {
	loc, e := time.LoadLocation(zone)
	if e != nil || zone == "" {
		if loc, e = time.LoadLocation(defaultZone); e != nil {
			loc = time.UTC
		}
	}
	return t.In(loc)
}

// formatTemperature quita ceros sobrantes ("12.00" → "12"); un valor no numérico se descarta.
func formatTemperature(raw string) string {
	v, e := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if e != nil || math.IsNaN(v) || math.Abs(v) > 9999.99 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func withSuffix(prefix, sep, suffix string) string {
	if suffix == "" {
		return prefix
	}
	return prefix + sep + suffix
}

// cleanLine elimina controles y colapsa espacios para que un nombre no rompa la bandeja.
func cleanLine(v string) string {
	v = strings.ToValidUTF8(v, "")
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

func truncate(v string, max int) string {
	if utf8.RuneCountInString(v) <= max {
		return v
	}
	return strings.TrimRightFunc(string([]rune(v)[:max-1]), unicode.IsSpace) + "…"
}
