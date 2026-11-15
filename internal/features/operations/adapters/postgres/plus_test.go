package postgres

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

func shift(n int, name, start, end string) domain.Shift {
	return domain.Shift{ID: uid(11, n), LocationID: site, Name: name, StartTime: start, EndTime: end}
}

func TestShiftsAndStockPostgres(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	catalog(t, ctx, s)
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	t.Run("turnos: roles, tenant, solape, idempotencia y versión", func(t *testing.T) {
		morning := shift(1, "Mañana", "06:00", "14:00")
		for _, user := range []string{"chef", "worker", "outside", "chef2"} {
			if _, e := s.CreateShift(ctx, user, org, "key-shift-denied", morning); e == nil {
				t.Fatal("creó turno sin ser administrador", user)
			}
		}
		if _, e := s.CreateShift(ctx, "admin", foreign, "key-shift-foreign", morning); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("administró otra empresa", e)
		}
		created, e := s.CreateShift(ctx, "admin", org, "key-shift-1", morning)
		if e != nil || created.Timezone != "America/Lima" || created.RowVersion != 1 || !created.Active {
			t.Fatalf("%+v %#v", created, e)
		}
		if again, e := s.CreateShift(ctx, "admin", org, "key-shift-1", morning); e != nil || again.ID != created.ID {
			t.Fatal("reintento", e)
		}
		if _, e := s.CreateShift(ctx, "admin", org, "key-shift-other-key", morning); e != nil {
			t.Fatal("misma operación con otra clave", e)
		}
		changed := morning
		changed.EndTime = "13:00"
		if _, e := s.CreateShift(ctx, "admin", org, "key-shift-changed", changed); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("mismo UUID con otros datos", e)
		}
		var invalid *domain.ValidationError
		if _, e := s.CreateShift(ctx, "admin", org, "key-shift-overlap", shift(2, "Cruce", "13:00", "15:00")); !errors.As(e, &invalid) {
			t.Fatal("aceptó turnos solapados", e)
		}
		if _, e := s.CreateShift(ctx, "admin", org, "key-shift-name", shift(3, "mañana", "15:00", "16:00")); !errors.As(e, &invalid) {
			t.Fatal("aceptó nombre repetido", e)
		}
		if _, e := s.CreateShift(ctx, "admin", org, "key-shift-afternoon", shift(4, "Tarde", "14:00", "22:00")); e != nil {
			t.Fatal(e)
		}
		night, e := s.CreateShift(ctx, "admin", org, "key-shift-night", shift(5, "Noche", "22:00", "06:00"))
		if e != nil {
			t.Fatal("turno que cruza medianoche", e)
		}
		if _, e = s.CreateShift(ctx, "admin", org, "key-shift-bad-hours", shift(6, "Igual", "08:00", "08:00")); !errors.As(e, &invalid) {
			t.Fatal("aceptó turno vacío", e)
		}
		page, e := s.Shifts(ctx, "worker", org, site, false, pagination.Request{Size: 2})
		if e != nil || len(page.Items) != 2 || page.NextToken == "" || page.Items[0].Name != "Mañana" {
			t.Fatal("lista por hora de inicio", page, e)
		}
		next, e := s.Shifts(ctx, "worker", org, site, false, pagination.Request{Size: 2, Token: page.NextToken})
		if e != nil || len(next.Items) != 1 {
			t.Fatal(next, e)
		}
		if _, e = s.Shifts(ctx, "worker", org, site, true, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("un worker vio inactivos", e)
		}
		if _, e = s.Shifts(ctx, "chef2", org, site, false, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("leyó sede no asignada", e)
		}
		if _, e = s.Shift(ctx, "outside", org, night.ID); e == nil {
			t.Fatal("leyó turno de otra empresa")
		}
		upd := domain.Shift{Name: "Noche", StartTime: "23:00", EndTime: "06:00", RowVersion: 2}
		if _, e = s.UpdateShift(ctx, "admin", org, night.ID, "key-shift-upd-stale", upd); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("versión obsoleta", e)
		}
		upd.RowVersion = 1
		if v, e := s.UpdateShift(ctx, "admin", org, night.ID, "key-shift-upd", upd); e != nil || v.RowVersion != 2 || v.StartTime != "23:00" {
			t.Fatal(v, e)
		}
		off, e := s.DeactivateShift(ctx, "admin", org, night.ID, "key-shift-off", 2)
		if e != nil || off.Active {
			t.Fatal(off, e)
		}
		if again, e := s.DeactivateShift(ctx, "admin", org, night.ID, "key-shift-off-2", 2); e != nil || again.Active {
			t.Fatal("desactivar es idempotente", e)
		}
		if _, e = s.CreateShift(ctx, "admin", org, "key-shift-night-2", shift(7, "Noche", "22:00", "06:00")); e != nil {
			t.Fatal("el nombre de un turno inactivo puede reutilizarse", e)
		}
	})

	t.Run("limpieza por turno respeta horario y zona de la sede", func(t *testing.T) {
		morning := uid(11, 1)
		day := time.Now().UTC().AddDate(0, 0, -2).Truncate(24 * time.Hour)
		at := func(hourUTC, minute int) time.Time {
			return day.Add(time.Duration(hourUTC)*time.Hour + time.Duration(minute)*time.Minute)
		}
		// Lima es UTC-5: 11:00Z son las 06:00 locales (inicio incluido) y 19:00Z las 14:00 (fin excluido).
		for i, tc := range []struct {
			when time.Time
			ok   bool
		}{{at(11, 0), true}, {at(15, 0), true}, {at(18, 59), true}, {at(10, 59), false}, {at(19, 0), false}, {at(6, 0), false}} {
			v := execution(200+i, tc.when)
			v.ShiftID = morning
			out, e := s.Execute(ctx, "chef", org, fmt.Sprintf("key-exec-shift-%d", i), v)
			if tc.ok && (e != nil || out.ShiftName != "Mañana") {
				t.Fatal("rechazó una hora dentro del turno", i, e)
			}
			if !tc.ok && !errors.Is(e, domain.ErrShiftOutOfHours) {
				t.Fatal("aceptó una hora fuera del turno", i, e)
			}
		}
		v := execution(210, at(15, 0))
		v.ShiftID = uid(11, 99)
		if _, e := s.Execute(ctx, "chef", org, "key-exec-shift-missing", v); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("turno inexistente", e)
		}
		if _, e := s.CreatePlan(ctx, "admin", org, "plan-per-shift", domain.CleaningPlan{ID: uid(7, 2), LocationID: site, Area: "Cocina", Task: "Cierre de turno", Frequency: "PER_SHIFT"}); e != nil {
			t.Fatal(e)
		}
		v = execution(211, at(15, 0))
		v.CleaningPlanID = uid(7, 2)
		var invalid *domain.ValidationError
		if _, e := s.Execute(ctx, "chef", org, "key-exec-per-shift", v); !errors.As(e, &invalid) || invalid.Fields["shiftId"] == "" {
			t.Fatal("el plan por turno exige turno", e)
		}
		v.ShiftID = morning
		if _, e := s.Execute(ctx, "chef", org, "key-exec-per-shift-ok", v); e != nil {
			t.Fatal(e)
		}
	})

	t.Run("movimientos: saldo, idempotencia, roles y concurrencia", func(t *testing.T) {
		if _, e := s.Receive(ctx, "chef", org, "key-stock-receipt", receipt(101, now)); e != nil {
			t.Fatal(e)
		}
		lots, e := s.Lots(ctx, "admin", org, site, pagination.Request{Size: 100})
		if e != nil {
			t.Fatal(e)
		}
		var lotID string
		for _, l := range lots.Items {
			if l.ReceiptItemID == uid(6, 101) {
				lotID = l.ID
				if l.ReceivedQuantity != "1.125" {
					t.Fatal("cantidad recibida", l.ReceivedQuantity)
				}
			}
		}
		move := func(n int, kind, qty, reason, note string) domain.StockMovement {
			return domain.StockMovement{ID: uid(12, n), LocationID: site, LotID: lotID, Type: kind, Quantity: qty, Reason: reason, Note: note, ClientOperationID: uid(13, n), ClientCreatedAt: now}
		}
		for _, user := range []string{"super", "worker", "outside", "chef2"} {
			if _, e := s.Move(ctx, user, org, "key-stock-denied", move(1, "CONSUMPTION", "0.125", "", "")); e == nil {
				t.Fatal("movió stock sin permiso", user)
			}
		}
		if _, e := s.Move(ctx, "chef", foreign, "key-stock-foreign", move(1, "CONSUMPTION", "0.125", "", "")); e == nil {
			t.Fatal("movió stock con tenant ajeno")
		}
		var invalid *domain.ValidationError
		for name, bad := range map[string]domain.StockMovement{
			"cero":           move(2, "CONSUMPTION", "0", "", ""),
			"negativa":       move(3, "CONSUMPTION", "-1", "", ""),
			"decimales":      move(4, "CONSUMPTION", "0.1234", "", ""),
			"sin motivo":     move(5, "DISCARD", "0.1", "", ""),
			"motivo raro":    move(6, "DISCARD", "0.1", "ROBO", ""),
			"otro sin nota":  move(7, "DISCARD", "0.1", "OTHER", ""),
			"consumo motivo": move(8, "CONSUMPTION", "0.1", "EXPIRED", ""),
			"tipo":           move(9, "TRANSFER", "0.1", "", ""),
		} {
			if _, e := s.Move(ctx, "chef", org, "key-stock-invalid", bad); !errors.As(e, &invalid) {
				t.Fatal("aceptó entrada inválida", name, e)
			}
		}
		if _, e := s.Move(ctx, "chef", org, "key-stock-too-much", move(10, "CONSUMPTION", "1.126", "", "")); !errors.Is(e, domain.ErrInsufficientStock) {
			t.Fatal("saldo negativo", e)
		}
		unknown := move(11, "CONSUMPTION", "0.1", "", "")
		unknown.LotID = uid(14, 1)
		if _, e := s.Move(ctx, "chef", org, "key-stock-unknown", unknown); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("lote inexistente", e)
		}
		first := move(12, "CONSUMPTION", "0.125", "", "")
		got, e := s.Move(ctx, "chef", org, "key-stock-first", first)
		if e != nil || got.BalanceAfter != "1.000" || got.PerformedByName != "Chef" || got.ProductName != "Producto" {
			t.Fatal(got, e)
		}
		for _, key := range []string{"key-stock-first", "key-stock-first-other-key"} {
			again, e := s.Move(ctx, "chef", org, key, first)
			if e != nil || again.ID != got.ID || again.BalanceAfter != "1.000" {
				t.Fatal("reintento", key, e)
			}
		}
		changed := first
		changed.Quantity = "0.5"
		if _, e := s.Move(ctx, "chef", org, "key-stock-changed", changed); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("mismo UUID con otra cantidad", e)
		}
		if _, e := s.Move(ctx, "chef2", org, "key-stock-other-user", first); e == nil {
			t.Fatal("otro usuario reutilizó el movimiento")
		}
		var balance, status string
		pool.QueryRow(ctx, `SELECT quantity_available::text,status FROM storage_lots WHERE id=$1`, lotID).Scan(&balance, &status)
		if balance != "1.000" || status != "AVAILABLE" {
			t.Fatal("saldo del lote", balance, status)
		}
		// El FIFO sigue listando el lote con su saldo actual.
		lots, _ = s.Lots(ctx, "chef", org, site, pagination.Request{Size: 100})
		for _, l := range lots.Items {
			if l.ID == lotID && (l.QuantityAvailable != "1.000" || l.ReceivedQuantity != "1.125") {
				t.Fatal("Lots no refleja el saldo", l)
			}
		}
		// Vencido: no se consume, se descarta.
		if _, e := pool.Exec(ctx, `UPDATE supply_receipt_items SET expiration_date=CURRENT_DATE-3 WHERE id=$1`, uid(6, 101)); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Move(ctx, "chef", org, "key-stock-expired", move(13, "CONSUMPTION", "0.1", "", "")); !errors.Is(e, domain.ErrLotExpired) {
			t.Fatal("consumió un lote vencido", e)
		}
		if _, e := s.Move(ctx, "chef", org, "key-stock-discard-part", move(14, "DISCARD", "0.4", "EXPIRED", "")); e != nil {
			t.Fatal(e)
		}
		last, e := s.Move(ctx, "chef", org, "key-stock-discard-all", move(15, "DISCARD", "0.6", "OTHER", "Se rompió el envase"))
		if e != nil || last.BalanceAfter != "0.000" {
			t.Fatal(last, e)
		}
		pool.QueryRow(ctx, `SELECT status FROM storage_lots WHERE id=$1`, lotID).Scan(&status)
		if status != "DISCARDED" {
			t.Fatal("estado del lote agotado", status)
		}
		if _, e := s.Move(ctx, "chef", org, "key-stock-after-zero", move(16, "DISCARD", "0.001", "EXPIRED", "")); !errors.Is(e, domain.ErrInsufficientStock) {
			t.Fatal("movió un lote agotado", e)
		}
		// Historial por lote y sede, paginado y filtrado.
		page, e := s.Movements(ctx, "admin", org, site, domain.MovementFilter{LotID: lotID}, pagination.Request{Size: 2})
		if e != nil || len(page.Items) != 2 || page.NextToken == "" || page.Items[0].ID != uid(12, 15) {
			t.Fatal("historial", page, e)
		}
		rest, e := s.Movements(ctx, "admin", org, site, domain.MovementFilter{LotID: lotID}, pagination.Request{Size: 2, Token: page.NextToken})
		if e != nil || len(rest.Items) != 1 || rest.Items[0].ID != uid(12, 12) {
			t.Fatal("página siguiente", rest, e)
		}
		if _, e = s.Movements(ctx, "admin", org, site, domain.MovementFilter{LotID: lotID, Type: "DISCARD"}, pagination.Request{Size: 2, Token: page.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
			t.Fatal("cursor de otro filtro", e)
		}
		discards, e := s.Movements(ctx, "chef", org, site, domain.MovementFilter{Type: "DISCARD"}, pagination.Request{})
		if e != nil || len(discards.Items) != 2 {
			t.Fatal(discards, e)
		}
		for _, user := range []string{"worker", "outside", "chef2"} {
			if _, e := s.Movements(ctx, user, org, site, domain.MovementFilter{}, pagination.Request{}); e == nil {
				t.Fatal("leyó historial sin permiso", user)
			}
		}
		if _, e := s.Movement(ctx, "chef2", org, got.ID); e == nil {
			t.Fatal("leyó movimiento de otra sede")
		}
		var audits int
		pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='stock_movement.created'`, org).Scan(&audits)
		if audits != 3 {
			t.Fatal("auditoría", audits)
		}
		if _, e := pool.Exec(ctx, `UPDATE stock_movements SET quantity=1 WHERE id=$1`, got.ID); e == nil {
			t.Fatal("el historial es editable")
		}
		if _, e := pool.Exec(ctx, `DELETE FROM stock_movements WHERE id=$1`, got.ID); e == nil {
			t.Fatal("el historial es eliminable")
		}
	})

	t.Run("dos descuentos simultáneos no dejan saldo negativo", func(t *testing.T) {
		if _, e := s.Receive(ctx, "chef", org, "key-stock-receipt-2", receipt(102, now)); e != nil {
			t.Fatal(e)
		}
		var lotID string
		pool.QueryRow(ctx, `SELECT id::text FROM storage_lots WHERE receipt_item_id=$1`, uid(6, 102)).Scan(&lotID)
		var wg sync.WaitGroup
		results := make(chan error, 4)
		for i := range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.Move(ctx, "chef", org, fmt.Sprintf("key-race-%d", i), domain.StockMovement{ID: uid(12, 300+i), LocationID: site, LotID: lotID, Type: "CONSUMPTION", Quantity: "0.5", ClientOperationID: uid(13, 300+i), ClientCreatedAt: now})
				results <- e
			}()
		}
		wg.Wait()
		close(results)
		ok := 0
		for e := range results {
			if e == nil {
				ok++
			} else if !errors.Is(e, domain.ErrInsufficientStock) {
				t.Fatal(e)
			}
		}
		var balance string
		pool.QueryRow(ctx, `SELECT quantity_available::text FROM storage_lots WHERE id=$1`, lotID).Scan(&balance)
		if ok != 2 || balance != "0.125" {
			t.Fatal("éxitos y saldo", ok, balance)
		}
	})
}

func TestShiftInUseCannotBeDeactivated(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	created, e := s.CreateShift(ctx, "admin", org, "key-inuse-shift", shift(31, "Mañana", "06:00", "14:00"))
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{
		`INSERT INTO checklist_templates(id,organization_id,name,area) VALUES('31000000-0000-0000-0000-000000000001',$1,'Control','Cocina')`,
		`INSERT INTO checklist_assignments(id,organization_id,location_id,template_id,frequency,shift_id) VALUES('32000000-0000-0000-0000-000000000001',$1,'` + site + `','31000000-0000-0000-0000-000000000001','DAILY','` + created.ID + `')`,
	} {
		if _, e = pool.Exec(ctx, q, org); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.DeactivateShift(ctx, "admin", org, created.ID, "key-inuse-off", 1); !errors.Is(e, domain.ErrShiftInUse) {
		t.Fatal("desactivó un turno con programación activa", e)
	}
	pool.Exec(ctx, `UPDATE checklist_assignments SET active=false`)
	if v, e := s.DeactivateShift(ctx, "admin", org, created.ID, "key-inuse-off-2", 1); e != nil || v.Active {
		t.Fatal(v, e)
	}
}
