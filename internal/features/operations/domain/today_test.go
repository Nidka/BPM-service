package domain

import (
	"testing"
	"time"
)

func lima(t *testing.T) *time.Location {
	t.Helper()
	z, e := time.LoadLocation("America/Lima")
	if e != nil {
		t.Fatal(e)
	}
	return z
}
func at(t *testing.T, v string) time.Time {
	t.Helper()
	p, e := time.Parse(time.RFC3339, v)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestTodayPlanPeriodByFrequency(t *testing.T) {
	zone := lima(t)
	now := at(t, "2026-11-20T15:00:00Z") // viernes 10:00 en Lima
	for _, tc := range []struct{ frequency, start, end string }{
		{"DAILY", "2026-11-20T05:00:00Z", "2026-11-21T05:00:00Z"},
		{"WEEKLY", "2026-11-16T05:00:00Z", "2026-11-23T05:00:00Z"},
		{"MONTHLY", "2026-11-01T05:00:00Z", "2026-12-01T05:00:00Z"},
	} {
		p, shift, e := TodayPlanPeriod(CleaningPlan{ID: "p", Frequency: tc.frequency}, now, zone, nil)
		if e != nil || shift != nil || p.PlanID != "p" || p.ShiftID != "" || !p.StartsAt.Equal(at(t, tc.start)) || !p.EndsAt.Equal(at(t, tc.end)) {
			t.Fatal(tc.frequency, p, shift, e)
		}
	}
	// La frontera de medianoche local: 04:59Z todavía es el día anterior en Lima.
	p, _, _ := TodayPlanPeriod(CleaningPlan{Frequency: "DAILY"}, at(t, "2026-11-20T04:59:59Z"), zone, nil)
	if !p.StartsAt.Equal(at(t, "2026-11-19T05:00:00Z")) {
		t.Fatal("frontera de zona", p)
	}
	// Horario de verano: el día de New York del cambio dura 25 horas.
	ny, _ := time.LoadLocation("America/New_York")
	p, _, _ = TodayPlanPeriod(CleaningPlan{Frequency: "DAILY"}, at(t, "2026-11-01T12:00:00Z"), ny, nil)
	if p.EndsAt.Sub(p.StartsAt) != 25*time.Hour {
		t.Fatal("DST", p)
	}
	if _, _, e := TodayPlanPeriod(CleaningPlan{Frequency: "ONCE"}, now, zone, nil); e == nil {
		t.Fatal("aceptó una frecuencia sin calendario")
	}
}

func TestPerShiftPicksOpenThenNextThenLatestClosed(t *testing.T) {
	zone := lima(t)
	morning := Shift{ID: "m", Name: "Mañana", StartTime: "06:00", EndTime: "14:00", Active: true}
	afternoon := Shift{ID: "a", Name: "Tarde", StartTime: "14:00", EndTime: "22:00", Active: true}
	night := Shift{ID: "n", Name: "Noche", StartTime: "22:00", EndTime: "06:00", Active: true}
	inactive := Shift{ID: "x", Name: "Viejo", StartTime: "08:00", EndTime: "10:00"}
	plan := CleaningPlan{ID: "p", Frequency: "PER_SHIFT"}
	for _, tc := range []struct {
		name, now, id, state, start, end string
		shifts                           []Shift
	}{
		{"abierto de la mañana (inicio incluido)", "2026-11-20T11:00:00Z", "m", "OPEN", "2026-11-20T11:00:00Z", "2026-11-20T19:00:00Z", []Shift{inactive, night, afternoon, morning}},
		{"el fin del turno es excluyente: pasa a la tarde", "2026-11-20T19:00:00Z", "a", "OPEN", "2026-11-20T19:00:00Z", "2026-11-21T03:00:00Z", []Shift{morning, afternoon, night}},
		{"turno nocturno abierto tras medianoche", "2026-11-20T07:30:00Z", "n", "OPEN", "2026-11-20T03:00:00Z", "2026-11-20T11:00:00Z", []Shift{morning, afternoon, night}},
		{"sin turno abierto: el próximo", "2026-11-20T09:00:00Z", "m", "UPCOMING", "2026-11-20T11:00:00Z", "2026-11-20T19:00:00Z", []Shift{morning}},
		{"todos cerrados: el que cerró más tarde", "2026-11-21T03:30:00Z", "a", "CLOSED", "2026-11-20T19:00:00Z", "2026-11-21T03:00:00Z", []Shift{morning, afternoon}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, shift, e := TodayPlanPeriod(plan, at(t, tc.now), zone, tc.shifts)
			if e != nil || shift == nil || shift.ID != tc.id || shift.State != tc.state || shift.Name == "" || p.ShiftID != tc.id {
				t.Fatalf("%+v %+v %v", p, shift, e)
			}
			if !p.StartsAt.Equal(at(t, tc.start)) || !p.EndsAt.Equal(at(t, tc.end)) {
				t.Fatalf("%+v", p)
			}
		})
	}
	// Sin turnos activos: turno nulo y período del día local; no hay ocurrencia que cumplir.
	p, shift, e := TodayPlanPeriod(plan, at(t, "2026-11-20T15:00:00Z"), zone, []Shift{inactive})
	if e != nil || shift != nil || !p.StartsAt.Equal(at(t, "2026-11-20T05:00:00Z")) || p.ShiftID != "" {
		t.Fatal(p, shift, e)
	}
	today := BuildToday(plan, p, shift, PlanActivity{Done: true, DoneByMe: true, LastExecutionID: "e"}, at(t, "2026-11-20T15:00:00Z"), zone, "America/Lima")
	if today.Done || today.DoneByMe || today.Overdue || today.Shift != nil || today.LastExecutionID != "" || NeedsActivity(plan, shift) {
		t.Fatal("PER_SHIFT sin turnos no puede estar hecho", today)
	}
}

func TestOverdueRules(t *testing.T) {
	zone := lima(t)
	open := &TodayShift{ID: "m", State: "OPEN"}
	for _, tc := range []struct {
		name  string
		plan  CleaningPlan
		done  bool
		now   string
		shift *TodayShift
		want  bool
	}{
		{"a la hora exacta no vence", CleaningPlan{Frequency: "DAILY", DueTime: "18:00"}, false, "2026-11-20T23:00:00Z", nil, false},
		{"un segundo después vence", CleaningPlan{Frequency: "DAILY", DueTime: "18:00"}, false, "2026-11-20T23:00:01Z", nil, true},
		{"antes de la hora no vence", CleaningPlan{Frequency: "DAILY", DueTime: "18:00"}, false, "2026-11-20T22:59:59Z", nil, false},
		{"hecho no vence", CleaningPlan{Frequency: "DAILY", DueTime: "18:00"}, true, "2026-11-21T03:00:00Z", nil, false},
		{"sin dueTime no vence", CleaningPlan{Frequency: "DAILY"}, false, "2026-11-21T03:00:00Z", nil, false},
		{"usa la hora local y no la UTC", CleaningPlan{Frequency: "DAILY", DueTime: "18:00"}, false, "2026-11-20T17:30:00Z", nil, false},
		{"semanal con dueTime no vence", CleaningPlan{Frequency: "WEEKLY", DueTime: "08:00"}, false, "2026-11-20T20:00:00Z", nil, false},
		{"mensual con dueTime no vence", CleaningPlan{Frequency: "MONTHLY", DueTime: "08:00"}, false, "2026-11-20T20:00:00Z", nil, false},
		{"por turno con dueTime vence", CleaningPlan{Frequency: "PER_SHIFT", DueTime: "10:00"}, false, "2026-11-20T15:30:00Z", open, true},
		{"por turno sin turno no vence", CleaningPlan{Frequency: "PER_SHIFT", DueTime: "10:00"}, false, "2026-11-20T15:30:00Z", nil, false},
		{"por turno aún no empieza no vence", CleaningPlan{Frequency: "PER_SHIFT", DueTime: "10:00"}, false, "2026-11-20T15:30:00Z", &TodayShift{State: "UPCOMING"}, false},
		{"por turno sin dueTime no vence", CleaningPlan{Frequency: "PER_SHIFT"}, false, "2026-11-20T15:30:00Z", open, false},
		{"hora inválida no vence", CleaningPlan{Frequency: "DAILY", DueTime: "25:99"}, false, "2026-11-21T03:00:00Z", nil, false},
	} {
		if got := PlanOverdue(tc.plan, tc.done, at(t, tc.now), zone, tc.shift); got != tc.want {
			t.Fatal(tc.name, got)
		}
	}
	// El reloj de pared decide, no la duración: el cambio de horario no desplaza la hora límite.
	ny, _ := time.LoadLocation("America/New_York")
	plan := CleaningPlan{Frequency: "DAILY", DueTime: "09:00"}
	if PlanOverdue(plan, false, at(t, "2026-11-01T14:00:00Z"), ny, nil) || !PlanOverdue(plan, false, at(t, "2026-11-01T14:00:01Z"), ny, nil) {
		t.Fatal("09:00 EST es 14:00Z el día del cambio de horario")
	}
}

func TestBuildTodayKeepsTeammateAndOwnActivityApart(t *testing.T) {
	zone := lima(t)
	now := at(t, "2026-11-20T23:30:00Z") // 18:30 en Lima
	plan := CleaningPlan{ID: "p", Frequency: "DAILY", DueTime: "18:00"}
	period, shift, _ := TodayPlanPeriod(plan, now, zone, nil)
	mine := at(t, "2026-11-20T20:00:00Z")
	for _, tc := range []struct {
		name         string
		activity     PlanActivity
		done, byMe   bool
		overdue      bool
		hasLast      bool
		lastResult   string
		lastExecutes *time.Time
	}{
		{"nadie lo hizo", PlanActivity{}, false, false, true, false, "", nil},
		{"lo hizo un compañero", PlanActivity{Done: true}, true, false, false, false, "", nil},
		{"lo hice yo", PlanActivity{Done: true, DoneByMe: true, LastExecutionID: "e1", LastResult: "OBSERVED", LastExecutedAt: &mine}, true, true, false, true, "OBSERVED", &mine},
		{"lo intenté sin completarlo", PlanActivity{LastExecutionID: "e2", LastResult: "NOT_COMPLETED", LastExecutedAt: &mine}, false, false, true, true, "NOT_COMPLETED", &mine},
	} {
		today := BuildToday(plan, period, shift, tc.activity, now, zone, "America/Lima")
		if today.Done != tc.done || today.DoneByMe != tc.byMe || today.Overdue != tc.overdue || (today.LastExecutionID != "") != tc.hasLast || today.LastResult != tc.lastResult || today.Timezone != "America/Lima" || today.Shift != nil {
			t.Fatal(tc.name, today)
		}
		if !today.PeriodStartsAt.Equal(at(t, "2026-11-20T05:00:00Z")) || !today.PeriodEndsAt.Equal(at(t, "2026-11-21T05:00:00Z")) {
			t.Fatal("período", today)
		}
	}
	if !CountsAsDone("COMPLETED") || !CountsAsDone("OBSERVED") || CountsAsDone("NOT_COMPLETED") {
		t.Fatal("resultados que cuentan")
	}
}

func TestResolveTimezoneFallsBackToLima(t *testing.T) {
	if z, name := ResolveTimezone("Asia/Tokyo"); name != "Asia/Tokyo" || z.String() != "Asia/Tokyo" {
		t.Fatal(name)
	}
	for _, bad := range []string{"", "No/Existe"} {
		if z, name := ResolveTimezone(bad); name != FallbackTimezone || z.String() != FallbackTimezone {
			t.Fatal(bad, name)
		}
	}
}
