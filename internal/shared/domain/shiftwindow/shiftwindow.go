// Package shiftwindow calcula ventanas horarias de turnos expresadas en hora local de una sede.
// Es puro: no conoce HTTP, SQL ni reglas de una feature. Un turno se define por una hora de
// inicio y otra de fin en minutos desde medianoche local; si el fin es menor que el inicio
// el turno cruza la medianoche (p. ej. 22:00-06:00). El fin es siempre excluyente.
package shiftwindow

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // Mantiene disponibles las zonas IANA también en imágenes mínimas.
)

// State describe la relación entre un instante y un turno de una sede.
type State string

const (
	// Open indica que el instante está dentro de la ocurrencia del turno.
	Open State = "OPEN"
	// Closed indica que la ocurrencia de hoy ya terminó.
	Closed State = "CLOSED"
	// Upcoming indica que la siguiente ocurrencia todavía no empieza.
	Upcoming State = "UPCOMING"
)

var ErrInvalid = errors.New("invalid shift window")

// Window es un turno diario en minutos desde medianoche local (0 a 1439).
type Window struct{ Start, End int }

// Parse convierte HH:MM estricto (24 horas) en minutos desde medianoche.
func Parse(value string) (int, error) {
	t, err := time.Parse("15:04", value)
	if err != nil || t.Format("15:04") != value {
		return 0, ErrInvalid
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Format es la inversa de Parse.
func Format(minutes int) string { return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60) }

// New valida y construye una ventana a partir de dos horas HH:MM. Inicio y fin no pueden coincidir.
func New(start, end string) (Window, error) {
	s, err := Parse(start)
	if err != nil {
		return Window{}, err
	}
	e, err := Parse(end)
	if err != nil || s == e {
		return Window{}, ErrInvalid
	}
	return Window{Start: s, End: e}, nil
}

// Crosses indica que el turno termina al día siguiente.
func (w Window) Crosses() bool { return w.End < w.Start }

// Contains indica si un minuto local pertenece al turno (inicio incluido, fin excluido).
func (w Window) Contains(minute int) bool {
	if w.Crosses() {
		return minute >= w.Start || minute < w.End
	}
	return minute >= w.Start && minute < w.End
}

// ContainsInstant evalúa un instante en la zona de la sede.
func (w Window) ContainsInstant(at time.Time, zone *time.Location) bool {
	local := at.In(zone)
	return w.Contains(local.Hour()*60 + local.Minute())
}

// Overlaps indica si dos turnos diarios comparten algún minuto.
func (w Window) Overlaps(o Window) bool {
	for m := w.Start; ; m = (m + 1) % 1440 {
		// Recorrer minutos es barato (máx. 1440) y evita errores con turnos que cruzan medianoche.
		if o.Contains(m) {
			return true
		}
		if (m+1)%1440 == w.End {
			return false
		}
	}
}

// OccurrenceOn devuelve el instante UTC de inicio y fin de la ocurrencia que empieza en el día
// local indicado. Usa el calendario local, por lo que respeta cambios de horario.
func (w Window) OccurrenceOn(zone *time.Location, year int, month time.Month, day int) (time.Time, time.Time) {
	start := time.Date(year, month, day, w.Start/60, w.Start%60, 0, 0, zone)
	endDay := day
	if w.Crosses() {
		endDay++
	}
	end := time.Date(year, month, endDay, w.End/60, w.End%60, 0, 0, zone)
	return start.UTC(), end.UTC()
}

// Select elige la ocurrencia relevante para now: la abierta; si no hay, la ya cerrada que empezó
// hoy (hora local); y si tampoco, la siguiente. Devuelve inicio y fin UTC (fin excluyente).
func (w Window) Select(now time.Time, zone *time.Location) (time.Time, time.Time, State) {
	local := now.In(zone)
	var lastStart, lastEnd time.Time
	haveClosed := false
	for offset := -1; offset <= 1; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 12, 0, 0, 0, zone)
		start, end := w.OccurrenceOn(zone, day.Year(), day.Month(), day.Day())
		switch {
		case !now.Before(start) && now.Before(end):
			return start, end, Open
		case !now.Before(end):
			if day.Day() == local.Day() && day.Month() == local.Month() {
				lastStart, lastEnd, haveClosed = start, end, true
			}
		default:
			if haveClosed {
				return lastStart, lastEnd, Closed
			}
			return start, end, Upcoming
		}
	}
	return lastStart, lastEnd, Closed
}
