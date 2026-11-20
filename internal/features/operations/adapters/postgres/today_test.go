package postgres

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// TestTodayPlansPostgres comprueba contra PostgreSQL real el estado de "hoy": solo activos, hecho por
// cualquier persona de la sede, datos propios únicamente, turnos y alcance del cursor.
func TestTodayPlansPostgres(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	catalog(t, ctx, s)
	for i, p := range []domain.CleaningPlan{
		{ID: uid(7, 2), LocationID: site, Area: "Cocina", Task: "Campana", Frequency: "WEEKLY"},
		{ID: uid(7, 3), LocationID: site, Area: "Cocina", Task: "Piso profundo", Frequency: "MONTHLY"},
		{ID: uid(7, 4), LocationID: site, Area: "Salón", Task: "Cierre de turno", Frequency: "PER_SHIFT", DueTime: "00:00"},
		{ID: uid(7, 5), LocationID: site, Area: "Almacén", Task: "Plan retirado", Frequency: "DAILY"},
		{ID: uid(7, 6), LocationID: otherSite, Area: "Otra sede", Task: "Ajena", Frequency: "DAILY"},
	} {
		if _, e := s.CreatePlan(ctx, "admin", org, fmt.Sprintf("plan-today-%d", i), p); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := pool.Exec(ctx, `UPDATE cleaning_plans SET active=false WHERE id=$1`, uid(7, 5)); e != nil {
		t.Fatal(e)
	}
	today := func(user, site string, r pagination.Request) pagination.Page[domain.TodayPlan] {
		t.Helper()
		page, e := s.TodayPlans(ctx, user, org, site, r)
		if e != nil {
			t.Fatal(user, e)
		}
		return page
	}
	byID := func(page pagination.Page[domain.TodayPlan]) map[string]domain.TodayPlan {
		out := map[string]domain.TodayPlan{}
		for _, p := range page.Items {
			out[p.ID] = p
		}
		return out
	}

	t.Run("solo activos y sin ejecuciones", func(t *testing.T) {
		page := today("worker", site, pagination.Request{Size: 100})
		rows := byID(page)
		if len(rows) != 4 || rows[uid(7, 5)].ID != "" || rows[uid(7, 6)].ID != "" {
			t.Fatal("solo los activos de la sede", len(rows))
		}
		all, e := s.Plans(ctx, "worker", org, site, pagination.Request{Size: 100})
		if e != nil || len(all.Items) != 5 {
			t.Fatal("la lista sin view conserva los inactivos", len(all.Items), e)
		}
		for _, p := range rows {
			if p.Today.Done || p.Today.DoneByMe || p.Today.LastExecutionID != "" || p.Today.Timezone != "America/Lima" || !p.Today.PeriodEndsAt.After(p.Today.PeriodStartsAt) {
				t.Fatal("estado inicial", p)
			}
		}
		// Sin turnos, el plan por turno no tiene ocurrencia: turno nulo, sin hacer y sin vencer.
		if sh := rows[uid(7, 4)].Today; sh.Shift != nil || sh.Overdue {
			t.Fatal("PER_SHIFT sin turnos", sh)
		}
		if due := rows[planID].Today; due.Overdue {
			t.Fatal("sin dueTime no vence", due)
		}
	})

	t.Run("hecho por un compañero no se atribuye a quien consulta", func(t *testing.T) {
		at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
		out, e := s.Execute(ctx, "worker2", org, "key-today-1", execution(301, at))
		if e != nil {
			t.Fatal(e)
		}
		mine := byID(today("worker", site, pagination.Request{Size: 100}))[planID].Today
		if !mine.Done || mine.DoneByMe || mine.LastExecutionID != "" || mine.LastResult != "" || mine.LastExecutedAt != nil {
			t.Fatal("el compañero completó; yo no", mine)
		}
		author := byID(today("worker2", site, pagination.Request{Size: 100}))[planID].Today
		if !author.Done || !author.DoneByMe || author.LastExecutionID != out.ID || author.LastResult != "COMPLETED" || author.LastExecutedAt == nil || !author.LastExecutedAt.Equal(at) {
			t.Fatal("autor", author)
		}
		chef := byID(today("chef", site, pagination.Request{Size: 100}))[planID].Today
		if !chef.Done || chef.DoneByMe || chef.LastExecutionID != "" {
			t.Fatal("el chef ve hecho pero no es suyo", chef)
		}
	})

	t.Run("NOT_COMPLETED no cuenta como hecho pero sí es mi última ejecución", func(t *testing.T) {
		v := execution(303, time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond))
		v.CleaningPlanID, v.Result, v.Observation = uid(7, 2), "NOT_COMPLETED", "Faltó producto"
		if _, e := s.Execute(ctx, "worker", org, "key-today-2", v); e != nil {
			t.Fatal(e)
		}
		own := byID(today("worker", site, pagination.Request{Size: 100}))[uid(7, 2)].Today
		if own.Done || own.DoneByMe || own.LastResult != "NOT_COMPLETED" || own.LastExecutionID != v.ID {
			t.Fatal("propia sin completar", own)
		}
		other := byID(today("worker2", site, pagination.Request{Size: 100}))[uid(7, 2)].Today
		if other.Done || other.DoneByMe || other.LastExecutionID != "" {
			t.Fatal("no se filtra a otras personas", other)
		}
		obs := execution(304, time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond))
		obs.CleaningPlanID, obs.Result, obs.Observation = uid(7, 2), "OBSERVED", "Con observación"
		if _, e := s.Execute(ctx, "chef", org, "key-today-3", obs); e != nil {
			t.Fatal(e)
		}
		after := byID(today("worker", site, pagination.Request{Size: 100}))[uid(7, 2)].Today
		if !after.Done || after.DoneByMe || after.LastResult != "NOT_COMPLETED" {
			t.Fatal("OBSERVED de otra persona cuenta como hecho", after)
		}
	})

	t.Run("fuera del período no cuenta", func(t *testing.T) {
		v := execution(305, time.Now().UTC().AddDate(0, -2, 0).Truncate(time.Microsecond))
		v.CleaningPlanID = uid(7, 3) // mensual: hace dos meses queda fuera del mes actual
		if _, e := s.Execute(ctx, "chef", org, "key-today-4", v); e != nil {
			t.Fatal(e)
		}
		monthly := byID(today("chef", site, pagination.Request{Size: 100}))[uid(7, 3)].Today
		if monthly.Done || monthly.DoneByMe || monthly.LastExecutionID != "" {
			t.Fatal("una ejecución de otro mes no cuenta", monthly)
		}
	})

	t.Run("por turno: turno abierto, hecho en ese turno y vencimiento", func(t *testing.T) {
		// Dos turnos que cubren las 24 horas locales: siempre hay uno abierto.
		for i, v := range []domain.Shift{shift(21, "Primera mitad", "00:00", "12:00"), shift(22, "Segunda mitad", "12:00", "00:00")} {
			if _, e := s.CreateShift(ctx, "admin", org, fmt.Sprintf("key-today-shift-%d", i), v); e != nil {
				t.Fatal(e)
			}
		}
		row := byID(today("worker", site, pagination.Request{Size: 100}))[uid(7, 4)].Today
		if row.Shift == nil || row.Shift.State != "OPEN" || row.Done {
			t.Fatal("turno abierto", row)
		}
		if row.Overdue != true {
			t.Fatal("dueTime 00:00 ya pasó y no está hecho", row)
		}
		v := execution(306, time.Now().UTC().Add(-time.Millisecond).Truncate(time.Microsecond))
		v.CleaningPlanID, v.ShiftID = uid(7, 4), row.Shift.ID
		if _, e := s.Execute(ctx, "worker2", org, "key-today-5", v); e != nil {
			t.Fatal(e)
		}
		done := byID(today("worker", site, pagination.Request{Size: 100}))[uid(7, 4)].Today
		if !done.Done || done.DoneByMe || done.Overdue || done.Shift == nil || done.LastExecutionID != "" {
			t.Fatal("hecho por el compañero en el turno abierto", done)
		}
	})

	t.Run("roles, sedes y tenant", func(t *testing.T) {
		for _, tc := range []struct {
			user, org, site string
			want            error
		}{
			{"chef2", org, site, domain.ErrDenied},           // asignado a otra sede
			{"outside", org, site, domain.ErrDenied},         // miembro de otra empresa
			{"worker", foreign, site, domain.ErrDenied},      // empresa ajena
			{"worker", org, uid(20, 99), domain.ErrNotFound}, // sede inexistente
		} {
			if _, e := s.TodayPlans(ctx, tc.user, tc.org, tc.site, pagination.Request{}); !errors.Is(e, tc.want) {
				t.Fatal(tc, e)
			}
		}
		if page, e := s.TodayPlans(ctx, "admin", org, otherSite, pagination.Request{}); e != nil || len(page.Items) != 1 || page.Items[0].ID != uid(7, 6) {
			t.Fatal("el administrador ve cualquier sede de su empresa", page, e)
		}
		if page, e := s.TodayPlans(ctx, "super", org, site, pagination.Request{}); e != nil || len(page.Items) == 0 {
			t.Fatal("SUPER_ADMIN sin membresía supervisa", e)
		}
	})

	t.Run("cursor por vista", func(t *testing.T) {
		first := today("worker", site, pagination.Request{Size: 2})
		if len(first.Items) != 2 || first.NextToken == "" {
			t.Fatal(first)
		}
		if _, e := s.Plans(ctx, "worker", org, site, pagination.Request{Size: 2, Token: first.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
			t.Fatal("cursor de today usado sin view", e)
		}
		plain, e := s.Plans(ctx, "worker", org, site, pagination.Request{Size: 2})
		if e != nil || plain.NextToken == "" {
			t.Fatal(e)
		}
		if _, e = s.TodayPlans(ctx, "worker", org, site, pagination.Request{Size: 2, Token: plain.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
			t.Fatal("cursor sin view usado en today", e)
		}
		second := today("worker", site, pagination.Request{Size: 2, Token: first.NextToken})
		if len(second.Items) != 2 || second.NextToken != "" || second.Items[0].ID == first.Items[0].ID {
			t.Fatal("segunda página de today", second)
		}
	})
}
