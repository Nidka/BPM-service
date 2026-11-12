package domain

import (
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/shared/domain/shiftwindow"
)

// Shift es un turno de una sede. StartTime/EndTime son HH:MM en la zona horaria de la sede;
// si EndTime es menor que StartTime cruza la medianoche. OpenNow se calcula al leer.
type Shift struct {
	ID, LocationID, Name, StartTime, EndTime, Timezone string
	Active, OpenNow                                    bool
	RowVersion                                         int
	CreatedAt                                          time.Time
}

// Window convierte las horas del turno en una ventana de cálculo.
func (s Shift) Window() (shiftwindow.Window, error) { return shiftwindow.New(s.StartTime, s.EndTime) }

// StockMovement es un consumo o descarte inmutable sobre un lote. Quantity y BalanceAfter son
// decimales exactos como cadena; el saldo nunca pasa por coma flotante.
type StockMovement struct {
	ID, LocationID, LotID, ProductName, Unit, BatchCode string
	Type, Quantity, Reason, Note, BalanceAfter          string
	PerformedBy, PerformedByName                        string
	ClientOperationID, RequestHash                      string
	ClientCreatedAt, CreatedAt                          time.Time
}

// MovementFilter acota el historial por lote y tipo; vacío significa sin filtro.
type MovementFilter struct{ LotID, Type string }

const (
	MovementConsumption = "CONSUMPTION"
	MovementDiscard     = "DISCARD"
)

// DiscardReasons enumera los motivos admitidos para descartar.
var DiscardReasons = []string{"EXPIRED", "DETERIORATED", "CONTAMINATION", "OTHER"}

func CanReadShifts(role string) bool { return CanReadCleaning(role) }

// CanMoveStock limita consumo y descarte al CHEF asignado; administración solo supervisa.
func CanMoveStock(role string) bool { return role == "CHEF" }

// NormalizeShift valida la creación de un turno; UUID de cliente, nombre y horas locales.
func NormalizeShift(v Shift) (Shift, error) {
	f := map[string]string{}
	id(f, "id", v.ID)
	id(f, "locationId", v.LocationID)
	v.ID = strings.ToLower(v.ID)
	v.LocationID = strings.ToLower(v.LocationID)
	validateShift(f, &v)
	v.Active, v.RowVersion, v.Timezone, v.OpenNow, v.CreatedAt = true, 1, "", false, time.Time{}
	return v, done(f)
}

// NormalizeShiftUpdate valida el cambio de nombre y horario con control de versión.
func NormalizeShiftUpdate(v Shift) (Shift, error) {
	f := map[string]string{}
	if v.RowVersion < 1 {
		f["rowVersion"] = "Consulta el turno antes de actualizarlo."
	}
	validateShift(f, &v)
	v.Timezone, v.OpenNow, v.CreatedAt = "", false, time.Time{}
	return v, done(f)
}

func validateShift(f map[string]string, v *Shift) {
	v.Name = strings.TrimSpace(v.Name)
	text(f, "name", v.Name, 2, 60)
	if _, e := shiftwindow.Parse(v.StartTime); e != nil {
		f["startTime"] = "Usa una hora válida con formato HH:MM."
	}
	if _, e := shiftwindow.Parse(v.EndTime); e != nil {
		f["endTime"] = "Usa una hora válida con formato HH:MM."
	}
	if f["startTime"] == "" && f["endTime"] == "" && v.StartTime == v.EndTime {
		f["endTime"] = "El turno debe durar al menos un minuto; si cruza la medianoche usa un fin menor que el inicio."
	}
}

// OverlapsAny informa si el turno comparte minutos con otro turno activo de la misma sede.
func (s Shift) OverlapsAny(others []Shift) bool {
	w, e := s.Window()
	if e != nil {
		return false
	}
	for _, o := range others {
		if o.ID == s.ID || !o.Active {
			continue
		}
		if ow, e := o.Window(); e == nil && w.Overlaps(ow) {
			return true
		}
	}
	return false
}

// NormalizeMovement valida un consumo o descarte antes de tocar el saldo.
func NormalizeMovement(v StockMovement, now time.Time) (StockMovement, error) {
	f := map[string]string{}
	id(f, "id", v.ID)
	id(f, "locationId", v.LocationID)
	id(f, "lotId", v.LotID)
	id(f, "clientOperationId", v.ClientOperationID)
	v.ID, v.LocationID, v.LotID = strings.ToLower(v.ID), strings.ToLower(v.LocationID), strings.ToLower(v.LotID)
	v.ClientOperationID = strings.ToLower(v.ClientOperationID)
	v.ClientCreatedAt = v.ClientCreatedAt.UTC().Truncate(time.Microsecond)
	timestamp(f, "clientCreatedAt", v.ClientCreatedAt, now)
	v.Note = strings.TrimSpace(v.Note)
	text(f, "note", v.Note, 0, 500)
	if q, e := Quantity(v.Quantity); e != nil {
		f["quantity"] = "Ingresa una cantidad mayor que cero, con hasta tres decimales."
	} else {
		v.Quantity = q
	}
	switch v.Type {
	case MovementConsumption:
		if v.Reason != "" {
			f["reason"] = "El consumo no lleva motivo de descarte."
		}
	case MovementDiscard:
		known := false
		for _, r := range DiscardReasons {
			known = known || r == v.Reason
		}
		if !known {
			f["reason"] = "Elige el motivo: vencido, deteriorado, contaminación u otro."
		} else if v.Reason == "OTHER" && v.Note == "" {
			f["note"] = "Explica el motivo del descarte."
		}
	default:
		f["type"] = "Elige consumo o descarte."
	}
	v.ProductName, v.Unit, v.BatchCode, v.BalanceAfter = "", "", "", ""
	v.PerformedBy, v.PerformedByName, v.RequestHash, v.CreatedAt = "", "", "", time.Time{}
	return v, done(f)
}

// NormalizeMovementFilter valida los filtros del historial.
func NormalizeMovementFilter(v MovementFilter) (MovementFilter, bool) {
	v.LotID = strings.ToLower(v.LotID)
	if v.LotID != "" && !ValidID(v.LotID) || v.Type != "" && v.Type != MovementConsumption && v.Type != MovementDiscard {
		return v, false
	}
	return v, true
}
