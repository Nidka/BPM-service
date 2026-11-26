package domain

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Límites de presentación del aviso push. Se cuentan en runas, no en bytes.
const (
	PushTitleMax = 60
	PushBodyMax  = 140
)

// Textos genéricos: solo se usan como respaldo para tipos de aviso desconocidos o entregas antiguas.
const (
	GenericPushTitle = "Tienes un aviso en BPM"
	GenericPushBody  = "Abre la aplicación para consultar tus avisos."
)

// Tipos de aviso que emite el evaluador de alertas.
const (
	AlertChecklistMissing     = "CHECKLIST_MISSING"
	AlertTemperatureDanger    = "TEMPERATURE_DANGER"
	AlertOrganizationInactive = "ORGANIZATION_INACTIVE"
	AlertTrainingAssigned     = "TRAINING_ASSIGNED"
	AlertIncidentAssigned     = "INCIDENT_ASSIGNED"
)

// PushFacts son datos de negocio ya resueltos por el adaptador. Nunca incluye datos personales de quien
// recibe ni de quien ejecutó la operación: solo sede, nombre de plantilla/producto/curso y valores medidos.
type PushFacts struct {
	AlertType  string
	SourceType string
	SourceID   string
	LocationID string
	// SiteName es el nombre de la sede; Subject el de la plantilla, el producto, el curso o la categoría de la
	// incidencia según el tipo.
	SiteName, Subject, Category string
	// Temperature llega como texto numérico de PostgreSQL (por ejemplo "12.00").
	Temperature string
	// InactivityDays es el umbral configurado en la regla (texto entero).
	InactivityDays string
	// DueAt y Zone formatean el vencimiento (capacitación o plazo de la acción correctiva) en la zona de la sede.
	DueAt *time.Time
	Zone  string
	// RecipientRole solo decide la ruta (ADMIN opera desde Empresa); no se muestra.
	RecipientRole string
}

// PushContent es el texto visible y los metadatos públicos de navegación del aviso.
type PushContent struct {
	Title, Body string
	// Type es el tipo de aviso; SourceType/EntityID identifican la fuente; SiteID la sede.
	Type, SourceType, EntityID, SiteID string
	// Route es una ruta estable de la aplicación; "avisos" abre la bandeja.
	Route string
}

const routeInbox = "avisos"

var routeID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// BuildPush arma el texto específico por tipo. Un tipo desconocido conserva el texto genérico y la bandeja.
// Los valores variables se limpian (sin saltos de línea ni controles) y se recortan por runas.
func BuildPush(f PushFacts) PushContent {
	site, subject, category := cleanText(f.SiteName), cleanText(f.Subject), cleanText(f.Category)
	out := PushContent{Title: GenericPushTitle, Body: GenericPushBody, Type: f.AlertType, SourceType: f.SourceType, Route: routeInbox}
	if routeID.MatchString(f.SourceID) {
		out.EntityID = strings.ToLower(f.SourceID)
	}
	if routeID.MatchString(f.LocationID) {
		out.SiteID = strings.ToLower(f.LocationID)
	}
	switch f.AlertType {
	case AlertTemperatureDanger:
		out.Title = withSuffix("Temperatura fuera de rango", " · ", site)
		out.Body = temperatureBody(subject, category, f.Temperature)
		if f.SourceType == "RECEIPT_ITEM" && out.EntityID != "" {
			out.Route = "control/operacion"
			if f.RecipientRole == "ADMIN" {
				out.Route = "empresa/operacion"
			}
		}
	case AlertChecklistMissing:
		out.Title = withSuffix("Control pendiente", ": ", subject)
		out.Body = withSuffix("Aún no se registra el control de hoy", " · ", site)
		if f.SourceType == "CHECKLIST_ASSIGNMENT" && out.EntityID != "" {
			out.Route = "control/programacion/" + out.EntityID
		}
	case AlertOrganizationInactive:
		out.Title = "Sin actividad reciente en BPM"
		out.Body = "Revisa la operación de la empresa."
		if d, e := strconv.Atoi(f.InactivityDays); e == nil && d > 0 {
			out.Body = "No hay actividad desde hace " + plural(d, "día", "días") + ". Revisa la operación."
		}
		out.Route = "empresa"
	case AlertTrainingAssigned:
		out.Title = withSuffix("Capacitación asignada", ": ", subject)
		out.Body = trainingBody(site, f.DueAt, f.Zone)
		out.Route = "aprender"
	case AlertIncidentAssigned:
		out.Title = withSuffix("Te asignaron una incidencia", ": ", subject)
		out.Body = incidentBody(site, f.DueAt, f.Zone)
		if f.SourceType == "INCIDENT" && out.EntityID != "" {
			out.Route = "control/incidencias"
			if f.RecipientRole == "ADMIN" {
				out.Route = "empresa/incidencias"
			}
		}
	}
	out.Title = Truncate(out.Title, PushTitleMax)
	out.Body = Truncate(out.Body, PushBodyMax)
	return out
}

// temperatureBody compone «Pollo (Carnes): 12 °C»; omite lo que no se conoce.
func temperatureBody(product, category, temperature string) string {
	value := formatTemperature(temperature)
	name := product
	if name != "" && category != "" {
		name += " (" + category + ")"
	}
	switch {
	case name != "" && value != "":
		return name + ": " + value + " °C"
	case value != "":
		return "Medición registrada: " + value + " °C"
	case name != "":
		return name + ": revisa la medición de la recepción."
	}
	return "Revisa la medición de la recepción."
}

func trainingBody(site string, due *time.Time, zone string) string {
	body := "Complétala en la Academia."
	if due != nil {
		loc, e := time.LoadLocation(zone)
		if e != nil {
			loc = time.UTC
		}
		body = "Vence el " + due.In(loc).Format("02/01/2006") + ". " + body
	}
	if site != "" {
		body = site + " · " + body
	}
	return body
}

// incidentBody compone «Sede · Plazo 25/11/2026 20:30» con el plazo en la zona de la sede; omite lo que no se conoce.
func incidentBody(site string, due *time.Time, zone string) string {
	deadline := ""
	if due != nil {
		loc, e := time.LoadLocation(zone)
		if e != nil {
			loc = time.UTC
		}
		deadline = "Plazo " + due.In(loc).Format("02/01/2006 15:04")
	}
	switch {
	case site != "" && deadline != "":
		return site + " · " + deadline
	case deadline != "":
		return deadline
	case site != "":
		return site + " · Revisa la acción correctiva asignada."
	}
	return "Revisa la acción correctiva asignada."
}

// formatTemperature quita ceros sobrantes ("12.00" → "12", "4.50" → "4.5"); un valor no numérico se descarta.
func formatTemperature(raw string) string {
	v, e := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if e != nil || math.IsNaN(v) || math.Abs(v) > 9999.99 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// withSuffix une prefijo y sufijo variable; sin sufijo devuelve solo el prefijo.
func withSuffix(prefix, sep, suffix string) string {
	if suffix == "" {
		return prefix
	}
	return prefix + sep + suffix
}

// cleanText elimina controles y colapsa espacios para que un nombre no rompa la notificación.
func cleanText(v string) string {
	if !utf8.ValidString(v) {
		v = strings.ToValidUTF8(v, "")
	}
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

// Truncate recorta a max runas agregando «…» sin partir un carácter multibyte.
func Truncate(v string, max int) string {
	if max <= 0 || utf8.RuneCountInString(v) <= max {
		return v
	}
	runes := []rune(v)
	return strings.TrimRightFunc(string(runes[:max-1]), unicode.IsSpace) + "…"
}
