package domain

import (
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/shared/domain/shiftwindow"
	_ "time/tzdata" // Mantiene zonas IANA disponibles también en la imagen mínima de producción.
)

var (
	ErrAssignmentRequired = errors.New("scheduled checklist required")
	ErrAssignmentStarted  = errors.New("scheduled checklist already started")
	// ErrShiftOutOfHours indica que el control por turno solo puede iniciarse dentro del horario del turno.
	ErrShiftOutOfHours = errors.New("shift is not open")
	// ErrTemplateOutdated indica que la versión pedida ya fue reemplazada por una publicación posterior.
	ErrTemplateOutdated = errors.New("checklist template version is outdated")
)

type Assignment struct {
	ID, TemplateID, TemplateName, TemplateVersionID, LocationID, LocationName string
	Frequency, DueTime, Timezone                                              string
	// Turno opcional: solo con frecuencia DAILY; las horas son HH:MM locales de la sede.
	ShiftID, ShiftName, ShiftStart, ShiftEnd string
	TemplateVersionNumber                    int
	Active                                   bool
	CreatedAt                                time.Time
	Period                                   AssignmentPeriod
	Run                                      *AssignedRun
}

type AssignmentPeriod struct {
	AssignmentID string
	StartsAt     time.Time
	EndsAt       *time.Time
	// ShiftState es OPEN, CLOSED o UPCOMING cuando la programación es por turno; vacío si no.
	ShiftState string
}

type AssignedRun struct {
	ID, Status, PerformedBy, PerformedByName string
	CreatedAt                                time.Time
	SubmittedAt                              *time.Time
}

// PeriodAt utiliza el calendario de la sede; nunca toma la fecha enviada por el dispositivo.
func (a Assignment) PeriodAt(now time.Time) (AssignmentPeriod, error) {
	p := AssignmentPeriod{AssignmentID: a.ID, StartsAt: a.CreatedAt}
	if a.ShiftID != "" {
		return a.shiftPeriod(p, now)
	}
	if a.Frequency == "ONCE" {
		return p, nil
	}
	zone, err := time.LoadLocation(a.Timezone)
	if err != nil {
		return p, err
	}
	local := now.In(zone)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	var end time.Time
	switch a.Frequency {
	case "DAILY":
		end = start.AddDate(0, 0, 1)
	case "WEEKLY":
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		end = start.AddDate(0, 0, 7)
	case "MONTHLY":
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, zone)
		end = start.AddDate(0, 1, 0)
	default:
		return p, errors.New("invalid assignment frequency")
	}
	p.StartsAt = start.UTC()
	utcEnd := end.UTC()
	p.EndsAt = &utcEnd
	return p, nil
}

// shiftPeriod elige la ocurrencia del turno (abierta, cerrada hoy o próxima) en la zona de la sede.
// La revisión única del período se evalúa dentro de esa ventana, no por día completo.
func (a Assignment) shiftPeriod(p AssignmentPeriod, now time.Time) (AssignmentPeriod, error) {
	zone, err := time.LoadLocation(a.Timezone)
	if err != nil {
		return p, err
	}
	window, err := shiftwindow.New(a.ShiftStart, a.ShiftEnd)
	if err != nil {
		return p, err
	}
	start, end, state := window.Select(now, zone)
	p.StartsAt, p.EndsAt, p.ShiftState = start, &end, string(state)
	return p, nil
}
