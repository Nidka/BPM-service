package domain

import (
	"time"

	"github.com/Nidka/BPM-service/internal/shared/domain/calendarperiod"
	"github.com/Nidka/BPM-service/internal/shared/domain/shiftwindow"
)

// ViewToday es el único valor admitido del parámetro view de GET /cleaning-plans.
const ViewToday = "today"

// FallbackTimezone se usa cuando la zona guardada de una sede no es una zona IANA válida.
const FallbackTimezone = "America/Lima"

// TodayShift identifica el turno que se evalúa para un plan PER_SHIFT.
type TodayShift struct{ ID, Name, State string }

// PlanToday es el estado de un plan en su período actual. No contiene nombres de otras personas:
// Done considera a cualquier persona de la sede; DoneByMe y Last* solo a quien consulta.
type PlanToday struct {
	PeriodStartsAt, PeriodEndsAt time.Time
	Timezone                     string
	Done, DoneByMe, Overdue      bool
	// Última ejecución propia dentro del período; vacío/nil si no hay.
	LastExecutionID, LastResult string
	LastExecutedAt              *time.Time
	Shift                       *TodayShift
}

// TodayPlan es un plan activo acompañado de su estado de hoy.
type TodayPlan struct {
	CleaningPlan
	Today PlanToday
}

// PlanPeriod es la ventana [StartsAt, EndsAt) en la que se busca actividad de un plan.
// ShiftID no vacío restringe la búsqueda a ejecuciones registradas con ese turno.
type PlanPeriod struct {
	PlanID           string
	StartsAt, EndsAt time.Time
	ShiftID          string
}

// PlanActivity resume las ejecuciones de un plan dentro de su período: Done cuenta las de
// cualquier persona; DoneByMe y Last* solo las de quien consulta.
type PlanActivity struct {
	Done, DoneByMe              bool
	LastExecutionID, LastResult string
	LastExecutedAt              *time.Time
}

// CountsAsDone indica si un resultado cumple el plan: NOT_COMPLETED no cuenta.
func CountsAsDone(result string) bool { return result == "COMPLETED" || result == "OBSERVED" }

// ResolveTimezone devuelve la zona de la sede o America/Lima si la guardada no es válida.
func ResolveTimezone(name string) (*time.Location, string) {
	if zone, e := time.LoadLocation(name); e == nil && name != "" {
		return zone, name
	}
	zone, _ := time.LoadLocation(FallbackTimezone)
	return zone, FallbackTimezone
}

// TodayPlanPeriod calcula el período del plan en la zona de la sede. DAILY, WEEKLY y MONTHLY usan el
// calendario compartido con checklists. PER_SHIFT usa un turno activo de la sede: el abierto; si no
// hay, el próximo por empezar; y si ya cerraron todos, el que cerró más tarde hoy. Sin turnos
// devuelve turno nulo: el plan no puede ejecutarse (POST exige shiftId), queda sin hacer y su período
// es el día local.
func TodayPlanPeriod(plan CleaningPlan, now time.Time, zone *time.Location, shifts []Shift) (PlanPeriod, *TodayShift, error) {
	p := PlanPeriod{PlanID: plan.ID}
	if plan.Frequency != "PER_SHIFT" {
		start, end, e := calendarperiod.Bounds(plan.Frequency, now, zone)
		if e != nil {
			return p, nil, e
		}
		p.StartsAt, p.EndsAt = start, end
		return p, nil, nil
	}
	type candidate struct {
		shift      Shift
		start, end time.Time
		state      shiftwindow.State
	}
	var open, upcoming, closed *candidate
	for _, s := range shifts {
		if !s.Active {
			continue
		}
		w, e := s.Window()
		if e != nil {
			continue
		}
		start, end, state := w.Select(now, zone)
		c := &candidate{s, start, end, state}
		switch state {
		case shiftwindow.Open:
			if open == nil || start.Before(open.start) {
				open = c
			}
		case shiftwindow.Upcoming:
			if upcoming == nil || start.Before(upcoming.start) {
				upcoming = c
			}
		default:
			if closed == nil || end.After(closed.end) {
				closed = c
			}
		}
	}
	chosen := open
	if chosen == nil {
		chosen = upcoming
	}
	if chosen == nil {
		chosen = closed
	}
	if chosen == nil {
		start, end, e := calendarperiod.Bounds("DAILY", now, zone)
		p.StartsAt, p.EndsAt = start, end
		return p, nil, e
	}
	p.StartsAt, p.EndsAt, p.ShiftID = chosen.start, chosen.end, chosen.shift.ID
	return p, &TodayShift{ID: chosen.shift.ID, Name: chosen.shift.Name, State: string(chosen.state)}, nil
}

// PlanOverdue indica si un plan sin hacer ya pasó su hora límite local. Solo aplican DAILY y
// PER_SHIFT con dueTime; la comparación usa la hora local de la sede (estrictamente mayor), por lo
// que un cambio de horario no la desplaza. Un turno que aún no empieza no puede estar vencido.
func PlanOverdue(plan CleaningPlan, done bool, now time.Time, zone *time.Location, shift *TodayShift) bool {
	if done || plan.DueTime == "" || (plan.Frequency != "DAILY" && plan.Frequency != "PER_SHIFT") {
		return false
	}
	if plan.Frequency == "PER_SHIFT" && (shift == nil || shift.State == string(shiftwindow.Upcoming)) {
		return false
	}
	due, e := shiftwindow.Parse(plan.DueTime)
	if e != nil {
		return false
	}
	local := now.In(zone)
	return local.Hour()*3600+local.Minute()*60+local.Second() > due*60
}

// NeedsActivity indica si hay que buscar ejecuciones del plan: un PER_SHIFT sin turno no tiene
// ocurrencia que cumplir.
func NeedsActivity(plan CleaningPlan, shift *TodayShift) bool {
	return plan.Frequency != "PER_SHIFT" || shift != nil
}

// BuildToday une período, turno y actividad en el estado que se expone. Un plan sin ocurrencia
// (PER_SHIFT sin turnos) queda sin hacer, sin vencer y con turno nulo.
func BuildToday(plan CleaningPlan, period PlanPeriod, shift *TodayShift, activity PlanActivity, now time.Time, zone *time.Location, zoneName string) PlanToday {
	out := PlanToday{PeriodStartsAt: period.StartsAt.UTC(), PeriodEndsAt: period.EndsAt.UTC(), Timezone: zoneName, Shift: shift}
	if !NeedsActivity(plan, shift) {
		return out
	}
	out.Done, out.DoneByMe = activity.Done, activity.DoneByMe
	out.LastExecutionID, out.LastResult, out.LastExecutedAt = activity.LastExecutionID, activity.LastResult, activity.LastExecutedAt
	out.Overdue = PlanOverdue(plan, out.Done, now, zone, shift)
	return out
}
