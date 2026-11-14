package domain

import (
	"errors"
	"time"
	_ "time/tzdata" // Mantiene zonas IANA disponibles también en la imagen mínima de producción.
)

var (
	ErrAssignmentRequired = errors.New("scheduled checklist required")
	ErrAssignmentStarted  = errors.New("scheduled checklist already started")
)

type Assignment struct {
	ID, TemplateID, TemplateName, TemplateVersionID, LocationID, LocationName string
	Frequency, DueTime, Timezone                                              string
	Active                                                                    bool
	CreatedAt                                                                 time.Time
	Period                                                                    AssignmentPeriod
	Run                                                                       *AssignedRun
}

type AssignmentPeriod struct {
	AssignmentID string
	StartsAt     time.Time
	EndsAt       *time.Time
}

type AssignedRun struct {
	ID, Status, PerformedBy, PerformedByName string
	CreatedAt                                time.Time
	SubmittedAt                              *time.Time
}

// PeriodAt utiliza el calendario de la sede; nunca toma la fecha enviada por el dispositivo.
func (a Assignment) PeriodAt(now time.Time) (AssignmentPeriod, error) {
	p := AssignmentPeriod{AssignmentID: a.ID, StartsAt: a.CreatedAt}
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
