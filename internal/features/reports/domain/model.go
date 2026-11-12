// Package domain define las reglas de los reportes BPM: períodos, cumplimiento,
// KPIs, avance de capacitación, constancias y resumen semanal. No conoce HTTP ni SQL.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Límites del contrato.
const (
	MaxComplianceDays  = 92
	MaxSummaryDays     = 366
	MaxExportDays      = 366
	MaxCertificateDays = 366
	MaxExportRows      = 50000
	DefaultThreshold   = 80.0
	MinThreshold       = 1.0
	MaxThreshold       = 100.0
	DateLayout         = "2006-01-02"
)

// Errores comparables que los adaptadores HTTP traducen a códigos estables.
var (
	ErrDenied              = errors.New("acceso denegado")
	ErrLocationNotFound    = errors.New("sede no disponible")
	ErrCourseNotFound      = errors.New("curso no disponible")
	ErrCertificateNotFound = errors.New("constancia no disponible")
	ErrNoData              = errors.New("sin ejecuciones en el período")
	ErrExportTooLarge      = errors.New("exportación demasiado grande")
)

// ThresholdError indica que el cumplimiento promedio no alcanza el umbral.
type ThresholdError struct{ Average, Threshold float64 }

func (e *ThresholdError) Error() string { return "cumplimiento por debajo del umbral" }

// ValidationError agrupa campos inválidos con sus nombres JSON.
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

// Actor es el usuario autenticado con el rol real obtenido de la membresía (o SUPER_ADMIN).
type Actor struct{ ID, Role string }

// CanReport permite consultar reportes; WORKER no tiene acceso.
func (a Actor) CanReport() bool {
	return a.Role == "ADMIN" || a.Role == "CHEF" || a.Role == "SUPER_ADMIN"
}

// CanAdminister permite configuración, constancias y reportes de personas.
func (a Actor) CanAdminister() bool { return a.Role == "ADMIN" || a.Role == "SUPER_ADMIN" }

// AllLocations indica si el actor ve todas las sedes; CHEF solo las asignadas.
func (a Actor) AllLocations() bool { return a.CanAdminister() }

// Scope delimita una consulta: tenant, alcance de sedes y período en fechas locales de cada sede.
type Scope struct {
	OrganizationID string
	UserID         string
	AllLocations   bool
	LocationID     string
	Period         Period
}

// Period es un rango inclusivo de fechas locales (medianoche UTC representa la fecha).
type Period struct{ From, To time.Time }

// Days devuelve la cantidad de días del período, ambos extremos incluidos.
func (p Period) Days() int { return int(p.To.Sub(p.From).Hours()/24) + 1 }

// ParsePeriod valida el par de fechas YYYY-MM-DD y su duración máxima.
func ParsePeriod(from, to string, maxDays int) (Period, error) {
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
		switch {
		case t.Before(f):
			fields["to"] = "La fecha final no puede ser anterior a la inicial."
		case int(t.Sub(f).Hours()/24)+1 > maxDays:
			fields["to"] = fmt.Sprintf("El período no puede superar %d días.", maxDays)
		}
	}
	if len(fields) > 0 {
		return Period{}, &ValidationError{Fields: fields}
	}
	return Period{From: f, To: t}, nil
}

// Lima es la zona por defecto de la plataforma; no tiene horario de verano.
var Lima = time.FixedZone("America/Lima", -5*3600)

// Round2 redondea a dos decimales.
func Round2(v float64) float64 {
	if v < 0 {
		return -Round2(-v)
	}
	return float64(int64(v*100+0.5)) / 100
}

func average(sum float64, runs int) *float64 {
	if runs == 0 {
		return nil
	}
	v := Round2(sum / float64(runs))
	return &v
}
