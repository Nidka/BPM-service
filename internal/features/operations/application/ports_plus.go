package application

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// ShiftStore agrupa la persistencia de turnos por sede.
type ShiftStore interface {
	Shift(context.Context, string, string) (domain.Shift, error)
	ActiveShifts(context.Context, string, string) ([]domain.Shift, error)
	CreateShift(context.Context, domain.Actor, string, domain.Shift) (domain.Shift, error)
	UpdateShift(context.Context, domain.Actor, string, domain.Shift) (domain.Shift, error)
	DeactivateShift(context.Context, domain.Actor, string, string) (domain.Shift, error)
	Shifts(context.Context, string, string, bool, pagination.Window) ([]domain.Shift, error)
}

// StockStore agrupa los movimientos sobre storage_lots y su historial.
type StockStore interface {
	LockLot(context.Context, string, string) (domain.Lot, error)
	ApplyMovement(context.Context, domain.Actor, string, domain.StockMovement) (domain.StockMovement, error)
	Movement(context.Context, string, string) (domain.StockMovement, error)
	Movements(context.Context, string, string, domain.MovementFilter, pagination.Window) ([]domain.StockMovement, error)
}
