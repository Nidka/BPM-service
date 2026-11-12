// Package domain define las reglas del registro de uso para investigación (RF-27): tipos y
// módulos permitidos, límites de lote y validación. No guarda cuerpos ni datos sanitarios.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Límites del contrato.
const (
	MaxBatchEvents  = 50
	MaxDurationSecs = 86400
	MaxPastAge      = 7 * 24 * time.Hour
	MaxFutureSkew   = 5 * time.Minute
	MaxSummaryDays  = 92
	DateLayout      = "2006-01-02"
)

// Tipos de evento admitidos.
const (
	SessionStart = "SESSION_START"
	SessionEnd   = "SESSION_END"
	ModuleVisit  = "MODULE_VISIT"
	AlertViewed  = "ALERT_VIEWED"
	AlertAttend  = "ALERT_ATTENDED"
)

// Modules son los módulos que se pueden registrar; APP se usa en eventos de sesión.
var Modules = []string{"APP", "HOME", "CHECKLISTS", "OPERATIONS", "INCIDENTS", "ALERTS", "TRAINING", "REPORTS", "SETTINGS"}

// Errores comparables.
var (
	ErrDenied              = errors.New("acceso denegado")
	ErrSessionConflict     = errors.New("sesión de otro usuario")
	ErrIdempotencyConflict = errors.New("evento reutilizado con otro contenido")
)

// ValidationError agrupa campos inválidos.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "campos inválidos" }

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	orgPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// ValidID indica si v es un UUID textual.
func ValidID(v string) bool { return uuidPattern.MatchString(v) }

// ValidOrganization valida el formato del identificador de organización.
func ValidOrganization(v string) bool { return orgPattern.MatchString(v) }

// Event es un evento de uso ya validado y normalizado (identificadores en minúscula, módulo resuelto).
type Event struct {
	ID, SessionID, Type, Module string
	OccurredAt                  time.Time
	DurationSeconds             *int
}

// Actor es el usuario con su rol; SUPER_ADMIN sin membresía no puede registrar uso.
type Actor struct{ ID, Role string }

// CanRecord indica si el rol tiene membresía real.
func (a Actor) CanRecord() bool { return a.Role == "ADMIN" || a.Role == "CHEF" || a.Role == "WORKER" }

// CanQuery indica si el rol puede consultar el uso agregado.
func (a Actor) CanQuery() bool { return a.Role == "ADMIN" || a.Role == "SUPER_ADMIN" }

func validModule(m string) bool {
	for _, v := range Modules {
		if v == m {
			return true
		}
	}
	return false
}

// NormalizeBatch valida un lote completo (todo o nada) y devuelve los eventos normalizados.
// Las claves de error usan el formato events[i].campo.
func NormalizeBatch(in []Event, now time.Time) ([]Event, error) {
	fields := map[string]string{}
	if len(in) == 0 {
		fields["events"] = "Envía al menos un evento."
	}
	if len(in) > MaxBatchEvents {
		fields["events"] = fmt.Sprintf("Envía como máximo %d eventos por lote.", MaxBatchEvents)
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	seen := map[string]bool{}
	out := make([]Event, 0, len(in))
	for i, e := range in {
		key := func(f string) string { return fmt.Sprintf("events[%d].%s", i, f) }
		id := strings.ToLower(e.ID)
		if !ValidID(e.ID) {
			fields[key("id")] = "Usa un UUID válido."
		} else if seen[id] {
			fields[key("id")] = "El identificador está repetido en el lote."
		}
		seen[id] = true
		if !ValidID(e.SessionID) {
			fields[key("sessionId")] = "Usa un UUID válido."
		}
		module := e.Module
		switch e.Type {
		case SessionStart, SessionEnd:
			if module == "" {
				module = "APP"
			}
		case ModuleVisit:
			if module == "" {
				fields[key("module")] = "Indica el módulo visitado."
			}
		case AlertViewed, AlertAttend:
			if module == "" {
				module = "ALERTS"
			}
		default:
			fields[key("type")] = "Tipo de evento no admitido."
		}
		if module != "" && !validModule(module) {
			fields[key("module")] = "Módulo no admitido."
		}
		if e.OccurredAt.IsZero() || e.OccurredAt.After(now.Add(MaxFutureSkew)) || e.OccurredAt.Before(now.Add(-MaxPastAge)) {
			fields[key("occurredAt")] = "La fecha debe estar dentro de los últimos 7 días y no en el futuro."
		}
		if e.DurationSeconds != nil {
			if (e.Type != ModuleVisit && e.Type != SessionEnd) || *e.DurationSeconds < 0 || *e.DurationSeconds > MaxDurationSecs {
				fields[key("durationSeconds")] = "Solo aplica a visitas de módulo y fin de sesión, entre 0 y 86400."
			}
		}
		out = append(out, Event{ID: id, SessionID: strings.ToLower(e.SessionID), Type: e.Type, Module: module, OccurredAt: e.OccurredAt.UTC().Truncate(time.Microsecond), DurationSeconds: e.DurationSeconds})
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	return out, nil
}

// Same indica si dos eventos son idénticos (para detectar la reutilización de un UUID).
func (e Event) Same(o Event) bool {
	if e.ID != o.ID || e.SessionID != o.SessionID || e.Type != o.Type || e.Module != o.Module || !e.OccurredAt.Equal(o.OccurredAt) {
		return false
	}
	if (e.DurationSeconds == nil) != (o.DurationSeconds == nil) {
		return false
	}
	return e.DurationSeconds == nil || *e.DurationSeconds == *o.DurationSeconds
}

// Lima es la zona con que se agrupan los días del resumen; no tiene horario de verano.
var Lima = time.FixedZone("America/Lima", -5*3600)

// Period es un rango inclusivo de fechas locales (medianoche UTC representa la fecha).
type Period struct{ From, To time.Time }

// ParsePeriod valida YYYY-MM-DD y la duración máxima.
func ParsePeriod(from, to string) (Period, error) {
	fields := map[string]string{}
	f, ferr := time.Parse(DateLayout, from)
	t, terr := time.Parse(DateLayout, to)
	if ferr != nil || f.Format(DateLayout) != from {
		fields["from"] = "Usa una fecha válida con formato AAAA-MM-DD."
	}
	if terr != nil || t.Format(DateLayout) != to {
		fields["to"] = "Usa una fecha válida con formato AAAA-MM-DD."
	}
	if len(fields) == 0 {
		if t.Before(f) {
			fields["to"] = "La fecha final no puede ser anterior a la inicial."
		} else if int(t.Sub(f).Hours()/24)+1 > MaxSummaryDays {
			fields["to"] = fmt.Sprintf("El período no puede superar %d días.", MaxSummaryDays)
		}
	}
	if len(fields) > 0 {
		return Period{}, &ValidationError{Fields: fields}
	}
	return Period{From: f, To: t}, nil
}

// Bounds devuelve los instantes UTC [inicio, fin) del período en hora de Lima.
func (p Period) Bounds() (time.Time, time.Time) {
	start := time.Date(p.From.Year(), p.From.Month(), p.From.Day(), 0, 0, 0, 0, Lima)
	end := time.Date(p.To.Year(), p.To.Month(), p.To.Day()+1, 0, 0, 0, 0, Lima)
	return start.UTC(), end.UTC()
}

// ModuleStat es el uso agregado de un módulo.
type ModuleStat struct {
	Module       string
	Visits       int
	UniqueUsers  int
	TotalSeconds int
}

// DayStat es la cantidad de sesiones iniciadas en un día local de Lima.
type DayStat struct {
	Date     string
	Sessions int
}

// Summary es el uso agregado de una empresa; no contiene datos de personas.
type Summary struct {
	From, To              string
	Sessions              int
	EndedSessions         int
	ActiveUsers           int
	AverageSessionSeconds *float64
	AlertsViewed          int
	AlertsAttended        int
	Modules               []ModuleStat
	Days                  []DayStat
}
