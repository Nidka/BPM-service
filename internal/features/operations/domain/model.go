// Package domain contiene las reglas de recepción, almacenamiento y limpieza.
package domain

import (
	"errors"
	"time"
)

var (
	ErrDenied   = errors.New("operation access denied")
	ErrNotFound = errors.New("operation resource not found")
	ErrConflict = errors.New("operation conflicts with confirmed data")
	// ErrInsufficientStock indica que el saldo del lote no cubre la cantidad pedida.
	ErrInsufficientStock = errors.New("stock balance is insufficient")
	// ErrLotExpired impide consumir un lote vencido; debe descartarse con motivo.
	ErrLotExpired = errors.New("storage lot is expired")
	// ErrShiftOutOfHours indica que el instante no pertenece al horario del turno.
	ErrShiftOutOfHours = errors.New("instant is outside the shift hours")
	// ErrShiftInUse impide desactivar un turno con programaciones activas.
	ErrShiftInUse = errors.New("shift has active schedules")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid operation input" }

type Actor struct{ ID, Role string }
type Supplier struct{ ID, Name, TaxID, ContactName, ContactPhone, ContactEmail, Address string }
type Product struct {
	ID, Name, Category, Unit               string
	RequiresTemperature                    bool
	MinimumTemperature, MaximumTemperature *float64
	RowVersion                             int
}
type TemperaturePolicy struct {
	RowVersion                             int
	RequiresTemperature                    bool
	MinimumTemperature, MaximumTemperature *float64
}
type Receipt struct {
	ID, LocationID, SupplierID, SupplierName, ReceivedBy, ReceivedByName string
	ReceivedAt                                                           time.Time
	DocumentReference, Notes, ClientOperationID, RequestHash             string
	ClientCreatedAt, CreatedAt                                           time.Time
	Items                                                                []ReceiptItem
}
type ReceiptItem struct {
	ID, ProductID, ProductName, Unit, BatchCode, ExpirationDate, Quantity string
	TemperatureCelsius, MinimumTemperature, MaximumTemperature            *float64
	Decision, RejectionReason, PackagingCondition, StorageLocation, Label string
}
type Lot struct {
	ID, LocationID, ReceiptItemID, ProductID, ProductName, Unit, BatchCode, ExpirationDate string
	ReceivedAt                                                                             time.Time
	StorageLocation, Label, QuantityAvailable, Status                                      string
	// ReceivedQuantity es lo aceptado en la recepción; QuantityAvailable es el saldo actual.
	ReceivedQuantity string
	Expired          bool
}
type CleaningPlan struct {
	ID, LocationID, Area, Task, Instructions, ProductName, Frequency, DueTime string
	Active                                                                    bool
}
type CleaningExecution struct {
	ID, LocationID, CleaningPlanID, Area, Task, ResponsibleUserID, ResponsibleName string
	ExecutedAt                                                                     time.Time
	Result, Observation, ClientOperationID, RequestHash                            string
	ClientCreatedAt, CreatedAt                                                     time.Time
	// Turno opcional; omitempty conserva el hash de solicitudes anteriores sin turno.
	ShiftID   string `json:",omitempty"`
	ShiftName string `json:",omitempty"`
}

func CanConfigure(role string) bool { return role == "ADMIN" || role == "SUPER_ADMIN" }
func CanReadStock(role string) bool { return CanConfigure(role) || role == "CHEF" }
func CanReceive(role string) bool   { return role == "CHEF" || role == "ADMIN" }
func CanExecuteCleaning(role string) bool {
	return role == "ADMIN" || role == "CHEF" || role == "WORKER"
}
func CanReadCleaning(role string) bool { return CanConfigure(role) || CanExecuteCleaning(role) }
