package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// decorateShift calcula si el turno está abierto ahora, en la zona horaria de su sede.
func (s *Service) decorateShift(v domain.Shift) domain.Shift {
	v.OpenNow = false
	zone, e := time.LoadLocation(v.Timezone)
	w, we := v.Window()
	if e == nil && we == nil && v.Active {
		v.OpenNow = w.ContainsInstant(s.now(), zone)
	}
	return v
}

// shiftConflicts rechaza nombres repetidos y horarios solapados entre turnos activos de la sede.
func shiftConflicts(ctx context.Context, db Store, org string, v domain.Shift) error {
	others, e := db.ActiveShifts(ctx, org, v.LocationID)
	if e != nil {
		return e
	}
	for _, o := range others {
		if o.ID != v.ID && strings.EqualFold(o.Name, v.Name) {
			return &domain.ValidationError{Fields: map[string]string{"name": "Ya existe un turno activo con ese nombre en la sede."}}
		}
	}
	if v.OverlapsAny(others) {
		return &domain.ValidationError{Fields: map[string]string{"startTime": "El horario se solapa con otro turno activo de la sede."}}
	}
	return nil
}

// CreateShift define un turno de una sede; solo administración, idempotente por UUID y clave.
func (s *Service) CreateShift(ctx context.Context, user, org, key string, input domain.Shift) (domain.Shift, error) {
	v, e := domain.NormalizeShift(input)
	if e != nil {
		return v, e
	}
	out, e := write(ctx, s, user, org, "shift", key, v, roleSite(org, v.LocationID, domain.CanConfigure), func(db Store, a domain.Actor) (domain.Shift, error) {
		previous, readErr := db.Shift(ctx, org, v.ID)
		if readErr == nil {
			if previous.LocationID != v.LocationID || previous.Name != v.Name || previous.StartTime != v.StartTime || previous.EndTime != v.EndTime {
				return v, domain.ErrConflict
			}
			return previous, nil
		}
		if !errors.Is(readErr, domain.ErrNotFound) {
			return v, readErr
		}
		if e := shiftConflicts(ctx, db, org, v); e != nil {
			return v, e
		}
		return db.CreateShift(ctx, a, org, v)
	})
	return s.decorateShift(out), e
}

// UpdateShift cambia nombre y horario con control optimista; no reescribe registros anteriores.
func (s *Service) UpdateShift(ctx context.Context, user, org, id, key string, input domain.Shift) (domain.Shift, error) {
	if !domain.ValidID(id) {
		return domain.Shift{}, domain.ErrNotFound
	}
	v, e := domain.NormalizeShiftUpdate(input)
	if e != nil {
		return v, e
	}
	v.ID = strings.ToLower(id)
	out, e := write(ctx, s, user, org, "shift-update:"+v.ID, key, v, roleSite(org, "", domain.CanConfigure), func(db Store, a domain.Actor) (domain.Shift, error) {
		current, e := db.Shift(ctx, org, v.ID)
		if e != nil {
			return v, e
		}
		if _, e = db.Location(ctx, a, org, current.LocationID); e != nil {
			return v, e
		}
		if !current.Active || current.RowVersion != v.RowVersion {
			return v, domain.ErrConflict
		}
		v.LocationID = current.LocationID
		if e = shiftConflicts(ctx, db, org, v); e != nil {
			return v, e
		}
		return db.UpdateShift(ctx, a, org, v)
	})
	return s.decorateShift(out), e
}

// DeactivateShift retira un turno sin programaciones activas; conserva el historial que lo usó.
func (s *Service) DeactivateShift(ctx context.Context, user, org, id, key string, rowVersion int) (domain.Shift, error) {
	if !domain.ValidID(id) {
		return domain.Shift{}, domain.ErrNotFound
	}
	if rowVersion < 1 {
		return domain.Shift{}, &domain.ValidationError{Fields: map[string]string{"rowVersion": "Consulta el turno antes de desactivarlo."}}
	}
	id = strings.ToLower(id)
	out, e := write(ctx, s, user, org, "shift-deactivate:"+id, key, rowVersion, roleSite(org, "", domain.CanConfigure), func(db Store, a domain.Actor) (domain.Shift, error) {
		current, e := db.Shift(ctx, org, id)
		if e != nil {
			return current, e
		}
		if _, e = db.Location(ctx, a, org, current.LocationID); e != nil {
			return current, e
		}
		if !current.Active {
			return current, nil
		}
		if current.RowVersion != rowVersion {
			return current, domain.ErrConflict
		}
		return db.DeactivateShift(ctx, a, org, id)
	})
	return s.decorateShift(out), e
}

// Shift consulta un turno de una sede a la que el actor tiene acceso.
func (s *Service) Shift(ctx context.Context, user, org, id string) (out domain.Shift, err error) {
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if !domain.CanReadShifts(a.Role) {
			return domain.ErrDenied
		}
		v, e := db.Shift(ctx, org, strings.ToLower(id))
		if e != nil {
			return e
		}
		if _, e = db.Location(ctx, a, org, v.LocationID); e != nil {
			return e
		}
		out = s.decorateShift(v)
		return nil
	})
	return
}

// Shifts lista los turnos de una sede; incluir inactivos es exclusivo de administración.
func (s *Service) Shifts(ctx context.Context, user, org, site string, includeInactive bool, r pagination.Request) (pagination.Page[domain.Shift], error) {
	if site == "" {
		return pagination.Page[domain.Shift]{}, pagination.ErrInvalid
	}
	resource := "shifts:active"
	if includeInactive {
		resource = "shifts:all"
	}
	page, e := list(ctx, s, user, org, resource, site, r, domain.CanReadShifts, func(db Store, a domain.Actor, w pagination.Window) ([]domain.Shift, error) {
		if includeInactive && !domain.CanConfigure(a.Role) {
			return nil, domain.ErrDenied
		}
		return db.Shifts(ctx, org, site, includeInactive, w)
	}, func(v domain.Shift) pagination.Position {
		return pagination.Position{Key: v.StartTime, ID: v.ID}
	})
	for i := range page.Items {
		page.Items[i] = s.decorateShift(page.Items[i])
	}
	return page, e
}

// Move registra un consumo o descarte sobre un lote. El saldo se descuenta en la misma transacción
// que el movimiento y su auditoría; nunca queda negativo y un reintento no duplica el hecho.
func (s *Service) Move(ctx context.Context, user, org, key string, input domain.StockMovement) (domain.StockMovement, error) {
	v, e := domain.NormalizeMovement(input, s.now())
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "stock-movement", key, v, roleSite(org, v.LocationID, domain.CanMoveStock), func(db Store, a domain.Actor) (domain.StockMovement, error) {
		v.RequestHash, e = requestHash(user, "stock-movement", v)
		if e != nil {
			return v, e
		}
		previous, readErr := db.Movement(ctx, org, v.ID)
		if readErr == nil {
			if previous.RequestHash != v.RequestHash || previous.PerformedBy != a.ID || previous.LocationID != v.LocationID || previous.ClientOperationID != v.ClientOperationID {
				return v, domain.ErrConflict
			}
			return previous, nil
		}
		if !errors.Is(readErr, domain.ErrNotFound) {
			return v, readErr
		}
		lot, e := db.LockLot(ctx, org, v.LotID)
		if e != nil {
			return v, e
		}
		if lot.LocationID != v.LocationID {
			return v, domain.ErrNotFound
		}
		if lot.Status != "AVAILABLE" {
			return v, domain.ErrInsufficientStock
		}
		if v.Type == domain.MovementConsumption && lot.Expired {
			return v, domain.ErrLotExpired
		}
		v.PerformedBy = a.ID
		return db.ApplyMovement(ctx, a, org, v)
	})
}

// Movement consulta un movimiento de una sede autorizada.
func (s *Service) Movement(ctx context.Context, user, org, id string) (out domain.StockMovement, err error) {
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if !domain.CanReadStock(a.Role) {
			return domain.ErrDenied
		}
		v, e := db.Movement(ctx, org, strings.ToLower(id))
		if e != nil {
			return e
		}
		if _, e = db.Location(ctx, a, org, v.LocationID); e != nil {
			return e
		}
		out = v
		return nil
	})
	return
}

// Movements lista el historial de una sede, opcionalmente de un lote o tipo.
func (s *Service) Movements(ctx context.Context, user, org, site string, filter domain.MovementFilter, r pagination.Request) (pagination.Page[domain.StockMovement], error) {
	if site == "" {
		return pagination.Page[domain.StockMovement]{}, pagination.ErrInvalid
	}
	filter, ok := domain.NormalizeMovementFilter(filter)
	if !ok {
		return pagination.Page[domain.StockMovement]{}, pagination.ErrInvalid
	}
	resource := "stock-movements:" + filter.LotID + ":" + filter.Type
	return list(ctx, s, user, org, resource, site, r, domain.CanReadStock, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.StockMovement, error) {
		return db.Movements(ctx, org, site, filter, w)
	}, func(v domain.StockMovement) pagination.Position {
		return pagination.Position{Key: v.CreatedAt.UTC().Format(time.RFC3339Nano), ID: v.ID}
	})
}

// checkShift valida el turno opcional de una limpieza: debe ser de la misma sede, estar activo y
// contener la hora de ejecución en la zona horaria de la sede. Un plan por turno lo exige.
func (s *Service) checkShift(ctx context.Context, db Store, a domain.Actor, org string, plan domain.CleaningPlan, v *domain.CleaningExecution) error {
	if v.ShiftID == "" {
		if plan.Frequency == "PER_SHIFT" {
			return &domain.ValidationError{Fields: map[string]string{"shiftId": "Este plan se realiza por turno: elige el turno."}}
		}
		return nil
	}
	shift, e := db.Shift(ctx, org, v.ShiftID)
	if e != nil {
		return e
	}
	if !shift.Active || shift.LocationID != v.LocationID {
		return domain.ErrNotFound
	}
	zone, e := db.Location(ctx, a, org, v.LocationID)
	if e != nil {
		return e
	}
	location, e := time.LoadLocation(zone)
	if e != nil {
		return e
	}
	window, e := shift.Window()
	if e != nil {
		return e
	}
	if !window.ContainsInstant(v.ExecutedAt, location) {
		return domain.ErrShiftOutOfHours
	}
	return nil
}
