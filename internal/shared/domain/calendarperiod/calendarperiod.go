// Package calendarperiod calcula el período calendario (día, semana o mes) que contiene un
// instante en la zona horaria de una sede. Es puro: no conoce HTTP, SQL ni reglas de una feature.
// Lo comparten checklists programados y planes de limpieza para que ambos cierren el período
// en el mismo instante.
package calendarperiod

import (
	"errors"
	"time"
	_ "time/tzdata" // Mantiene disponibles las zonas IANA también en imágenes mínimas.
)

// ErrFrequency indica una frecuencia que no tiene calendario (solo DAILY, WEEKLY y MONTHLY).
var ErrFrequency = errors.New("invalid assignment frequency")

// Bounds devuelve el inicio (inclusivo) y el fin (excluyente) UTC del período de frecuencia
// DAILY, WEEKLY (semana que empieza el lunes) o MONTHLY que contiene now, calculados con el
// calendario local de zone. Al razonar con fechas locales respeta los cambios de horario.
func Bounds(frequency string, now time.Time, zone *time.Location) (time.Time, time.Time, error) {
	local := now.In(zone)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	var end time.Time
	switch frequency {
	case "DAILY":
		end = start.AddDate(0, 0, 1)
	case "WEEKLY":
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		end = start.AddDate(0, 0, 7)
	case "MONTHLY":
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, zone)
		end = start.AddDate(0, 1, 0)
	default:
		return time.Time{}, time.Time{}, ErrFrequency
	}
	return start.UTC(), end.UTC(), nil
}
