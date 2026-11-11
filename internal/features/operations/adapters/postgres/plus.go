package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

// RequireSchemaPlus rechaza arrancar si falta la migración V13 (turnos y movimientos de stock).
func (r *Repository) RequireSchemaPlus(ctx context.Context) error {
	rows, e := r.pool.Query(ctx, `SELECT s.row_version,m.balance_after,e.shift_id,a.shift_id,c.shift_id,v.change_note FROM location_shifts s CROSS JOIN stock_movements m CROSS JOIN cleaning_executions e CROSS JOIN checklist_assignments a CROSS JOIN checklist_runs c CROSS JOIN checklist_template_versions v LIMIT 0`)
	if e != nil {
		return errors.New("operations shifts and stock migration V13 is required")
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	var valid bool
	e = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index x JOIN pg_class c ON c.oid=x.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='checklist_versions_one_published_uq' AND n.nspname=current_schema() AND x.indisunique AND x.indisvalid)`).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return errors.New("operations shifts and stock migration V13 is required")
	}
	return nil
}

const shiftFields = `s.id::text,s.location_id::text,s.name,to_char(s.start_time,'HH24:MI'),to_char(s.end_time,'HH24:MI'),l.timezone,s.active,s.row_version,s.created_at`
const shiftFrom = ` FROM location_shifts s JOIN locations l ON l.organization_id=s.organization_id AND l.id=s.location_id `

func scanShift(row pgx.Row) (v domain.Shift, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.Name, &v.StartTime, &v.EndTime, &v.Timezone, &v.Active, &v.RowVersion, &v.CreatedAt)
	v.CreatedAt = v.CreatedAt.UTC()
	return v, missing(e)
}
func (s *store) Shift(ctx context.Context, org, id string) (domain.Shift, error) {
	return scanShift(s.tx.QueryRow(ctx, `SELECT `+shiftFields+shiftFrom+`WHERE s.organization_id=$1 AND s.id=$2`, org, id))
}
func (s *store) ActiveShifts(ctx context.Context, org, site string) ([]domain.Shift, error) {
	rows, e := s.tx.Query(ctx, `SELECT `+shiftFields+shiftFrom+`WHERE s.organization_id=$1 AND s.location_id=$2 AND s.active ORDER BY s.start_time,s.id`, org, site)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Shift{}
	for rows.Next() {
		v, e := scanShift(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) CreateShift(ctx context.Context, a domain.Actor, org string, v domain.Shift) (domain.Shift, error) {
	_, e := s.tx.Exec(ctx, `INSERT INTO location_shifts(id,organization_id,location_id,name,start_time,end_time,created_by,updated_by) VALUES($1,$2,$3,$4,$5::time,$6::time,$7,$7)`, v.ID, org, v.LocationID, v.Name, v.StartTime, v.EndTime, a.ID)
	if e != nil {
		return v, conflict(e)
	}
	if e = s.Audit(ctx, a, org, "shift.created", v.ID); e != nil {
		return v, e
	}
	return s.Shift(ctx, org, v.ID)
}
func (s *store) UpdateShift(ctx context.Context, a domain.Actor, org string, v domain.Shift) (domain.Shift, error) {
	tag, e := s.tx.Exec(ctx, `UPDATE location_shifts SET name=$3,start_time=$4::time,end_time=$5::time,row_version=row_version+1,updated_by=$6 WHERE organization_id=$1 AND id=$2 AND active AND row_version=$7`, org, v.ID, v.Name, v.StartTime, v.EndTime, a.ID, v.RowVersion)
	if e != nil {
		return v, conflict(e)
	}
	if tag.RowsAffected() != 1 {
		return v, domain.ErrConflict
	}
	if e = s.Audit(ctx, a, org, "shift.updated", v.ID); e != nil {
		return v, e
	}
	return s.Shift(ctx, org, v.ID)
}

// DeactivateShift bloquea el turno antes de contar programaciones, de modo que una alta concurrente
// (que toma FOR SHARE sobre el turno) quede serializada y nunca apunte a un turno inactivo.
func (s *store) DeactivateShift(ctx context.Context, a domain.Actor, org, id string) (domain.Shift, error) {
	var locked string
	if e := s.tx.QueryRow(ctx, `SELECT id::text FROM location_shifts WHERE organization_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&locked); e != nil {
		return domain.Shift{}, missing(e)
	}
	var used int
	if e := s.tx.QueryRow(ctx, `SELECT count(*) FROM checklist_assignments WHERE organization_id=$1 AND shift_id=$2 AND active`, org, id).Scan(&used); e != nil {
		return domain.Shift{}, e
	}
	if used > 0 {
		return domain.Shift{}, domain.ErrShiftInUse
	}
	if _, e := s.tx.Exec(ctx, `UPDATE location_shifts SET active=false,row_version=row_version+1,updated_by=$3 WHERE organization_id=$1 AND id=$2`, org, id, a.ID); e != nil {
		return domain.Shift{}, e
	}
	if e := s.Audit(ctx, a, org, "shift.deactivated", id); e != nil {
		return domain.Shift{}, e
	}
	return s.Shift(ctx, org, id)
}
func (s *store) Shifts(ctx context.Context, org, site string, all bool, w pagination.Window) ([]domain.Shift, error) {
	if e := catalogWindow(w); e != nil {
		return nil, e
	}
	if w.After.ID != "" {
		if _, e := time.Parse("15:04", w.After.Key); e != nil || !domain.ValidID(w.After.ID) {
			return nil, pagination.ErrInvalid
		}
	}
	rows, e := s.tx.Query(ctx, `SELECT `+shiftFields+shiftFrom+`WHERE s.organization_id=$1 AND s.location_id=$2 AND ($6 OR s.active) AND strpos(lower(s.name),lower($3))>0 AND ($5='' OR (s.start_time,s.id)>(NULLIF($4,'')::time,NULLIF($5,'')::uuid)) ORDER BY s.start_time,s.id LIMIT $7`, org, site, w.Query, w.After.Key, w.After.ID, all, limit(w))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Shift{}
	for rows.Next() {
		v, e := scanShift(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const lotSelect = `SELECT l.id::text,l.location_id::text,l.receipt_item_id::text,i.product_id::text,p.name,p.unit,COALESCE(i.batch_code,''),COALESCE(i.expiration_date::text,''),r.received_at,l.storage_location,COALESCE(l.label,''),l.quantity_available::text,l.status,COALESCE(i.expiration_date < (CURRENT_TIMESTAMP AT TIME ZONE loc.timezone)::date,false),i.quantity::text
 FROM storage_lots l JOIN supply_receipt_items i ON i.organization_id=l.organization_id AND i.id=l.receipt_item_id JOIN supply_receipts r ON r.organization_id=i.organization_id AND r.id=i.receipt_id JOIN products p ON p.organization_id=i.organization_id AND p.id=i.product_id JOIN locations loc ON loc.organization_id=l.organization_id AND loc.id=l.location_id `

// LockLot bloquea la fila del lote hasta el fin de la transacción: dos descuentos simultáneos
// sobre el mismo lote se serializan y el segundo ve el saldo ya descontado.
func (s *store) LockLot(ctx context.Context, org, id string) (v domain.Lot, e error) {
	e = s.tx.QueryRow(ctx, lotSelect+`WHERE l.organization_id=$1 AND l.id=$2 FOR UPDATE OF l`, org, id).Scan(&v.ID, &v.LocationID, &v.ReceiptItemID, &v.ProductID, &v.ProductName, &v.Unit, &v.BatchCode, &v.ExpirationDate, &v.ReceivedAt, &v.StorageLocation, &v.Label, &v.QuantityAvailable, &v.Status, &v.Expired, &v.ReceivedQuantity)
	v.ReceivedAt = v.ReceivedAt.UTC()
	return v, missing(e)
}

// ApplyMovement descuenta el saldo con una comparación numérica exacta en SQL (sin coma flotante),
// registra el movimiento inmutable y su auditoría. Si el saldo no alcanza, nada se modifica.
func (s *store) ApplyMovement(ctx context.Context, a domain.Actor, org string, v domain.StockMovement) (domain.StockMovement, error) {
	if !hashPattern.MatchString(v.RequestHash) {
		return v, domain.ErrConflict
	}
	closed := "CONSUMED"
	if v.Type == domain.MovementDiscard {
		closed = "DISCARDED"
	}
	var balance string
	e := s.tx.QueryRow(ctx, `UPDATE storage_lots SET quantity_available=quantity_available-$4::numeric,status=CASE WHEN quantity_available-$4::numeric=0 THEN $5::varchar(20) ELSE status END,updated_by=$6
 WHERE organization_id=$1 AND id=$2 AND location_id=$3 AND status='AVAILABLE' AND quantity_available>=$4::numeric RETURNING quantity_available::text`, org, v.LotID, v.LocationID, v.Quantity, closed, a.ID).Scan(&balance)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, domain.ErrInsufficientStock
	}
	if e != nil {
		return v, e
	}
	_, e = s.tx.Exec(ctx, `INSERT INTO stock_movements(id,organization_id,location_id,storage_lot_id,movement_type,quantity,balance_after,reason,note,performed_by,client_operation_id,client_created_at,request_hash,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6::numeric,$7::numeric,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$10,$10)`, v.ID, org, v.LocationID, v.LotID, v.Type, v.Quantity, balance, v.Reason, v.Note, a.ID, v.ClientOperationID, v.ClientCreatedAt, v.RequestHash)
	if e != nil {
		return v, conflict(e)
	}
	if e = s.Audit(ctx, a, org, "stock_movement.created", v.ID); e != nil {
		return v, e
	}
	return s.Movement(ctx, org, v.ID)
}

const movementFields = `m.id::text,m.location_id::text,m.storage_lot_id::text,p.name,p.unit,COALESCE(i.batch_code,''),m.movement_type,m.quantity::text,COALESCE(m.reason,''),COALESCE(m.note,''),m.balance_after::text,m.performed_by,u.display_name,m.client_operation_id::text,m.request_hash,m.client_created_at,m.created_at`
const movementFrom = ` FROM stock_movements m JOIN storage_lots l ON l.organization_id=m.organization_id AND l.id=m.storage_lot_id JOIN supply_receipt_items i ON i.organization_id=l.organization_id AND i.id=l.receipt_item_id JOIN products p ON p.organization_id=i.organization_id AND p.id=i.product_id JOIN application_users u ON u.id=m.performed_by `

func scanMovement(row pgx.Row) (v domain.StockMovement, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.LotID, &v.ProductName, &v.Unit, &v.BatchCode, &v.Type, &v.Quantity, &v.Reason, &v.Note, &v.BalanceAfter, &v.PerformedBy, &v.PerformedByName, &v.ClientOperationID, &v.RequestHash, &v.ClientCreatedAt, &v.CreatedAt)
	v.ClientCreatedAt = v.ClientCreatedAt.UTC()
	v.CreatedAt = v.CreatedAt.UTC()
	return v, missing(e)
}
func (s *store) Movement(ctx context.Context, org, id string) (domain.StockMovement, error) {
	return scanMovement(s.tx.QueryRow(ctx, `SELECT `+movementFields+movementFrom+`WHERE m.organization_id=$1 AND m.id=$2`, org, id))
}
func (s *store) Movements(ctx context.Context, org, site string, f domain.MovementFilter, w pagination.Window) ([]domain.StockMovement, error) {
	after, e := dateWindow(w)
	if e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+movementFields+movementFrom+`WHERE m.organization_id=$1 AND m.location_id=$2 AND ($7='' OR m.storage_lot_id=NULLIF($7,'')::uuid) AND ($8='' OR m.movement_type=$8)
 AND strpos(lower(p.name || ' ' || COALESCE(i.batch_code,'') || ' ' || COALESCE(m.note,'')),lower($3))>0 AND ($5='' OR (m.created_at,m.id)<($4,NULLIF($5,'')::uuid)) ORDER BY m.created_at DESC,m.id DESC LIMIT $6`, org, site, w.Query, after, w.After.ID, limit(w), f.LotID, f.Type)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.StockMovement{}
	for rows.Next() {
		v, e := scanMovement(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
