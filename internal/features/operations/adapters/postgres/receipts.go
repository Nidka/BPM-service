package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strings"
	"time"
)

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const receiptFields = `r.id::text,r.location_id::text,r.supplier_id::text,s.name,r.received_by,u.display_name,r.received_at,COALESCE(r.document_reference,''),COALESCE(r.notes,''),r.client_operation_id::text,r.client_created_at,r.created_at,COALESCE(r.request_hash,'')`

func scanReceipt(row pgx.Row) (v domain.Receipt, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.SupplierID, &v.SupplierName, &v.ReceivedBy, &v.ReceivedByName, &v.ReceivedAt, &v.DocumentReference, &v.Notes, &v.ClientOperationID, &v.ClientCreatedAt, &v.CreatedAt, &v.RequestHash)
	v.ReceivedAt = v.ReceivedAt.UTC()
	v.ClientCreatedAt = v.ClientCreatedAt.UTC()
	v.CreatedAt = v.CreatedAt.UTC()
	return v, missing(e)
}
func (s *store) receiptItems(ctx context.Context, org string, receipts []domain.Receipt) error {
	ids := make([]string, 0, len(receipts))
	positions := map[string]int{}
	for i := range receipts {
		ids = append(ids, receipts[i].ID)
		positions[receipts[i].ID] = i
		receipts[i].Items = []domain.ReceiptItem{}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, e := s.tx.Query(ctx, `SELECT i.receipt_id::text,i.id::text,i.product_id::text,p.name,p.unit,COALESCE(i.batch_code,''),COALESCE(i.expiration_date::text,''),i.quantity::text,i.temperature_celsius::double precision,i.minimum_temperature::double precision,i.maximum_temperature::double precision,i.decision,COALESCE(i.rejection_reason,''),COALESCE(i.packaging_condition,''),COALESCE(i.storage_location,l.storage_location,''),COALESCE(i.label,l.label,'')
 FROM supply_receipt_items i JOIN products p ON p.organization_id=i.organization_id AND p.id=i.product_id
 LEFT JOIN storage_lots l ON l.organization_id=i.organization_id AND l.receipt_item_id=i.id
 WHERE i.organization_id=$1 AND i.receipt_id=ANY($2::uuid[]) ORDER BY i.receipt_id,COALESCE(i.display_order,0),i.id`, org, ids)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var receiptID string
		var v domain.ReceiptItem
		e = rows.Scan(&receiptID, &v.ID, &v.ProductID, &v.ProductName, &v.Unit, &v.BatchCode, &v.ExpirationDate, &v.Quantity, &v.TemperatureCelsius, &v.MinimumTemperature, &v.MaximumTemperature, &v.Decision, &v.RejectionReason, &v.PackagingCondition, &v.StorageLocation, &v.Label)
		if e != nil {
			return e
		}
		receipts[positions[receiptID]].Items = append(receipts[positions[receiptID]].Items, v)
	}
	return rows.Err()
}
func (s *store) Receipt(ctx context.Context, org, id string) (domain.Receipt, error) {
	v, e := scanReceipt(s.tx.QueryRow(ctx, `SELECT `+receiptFields+` FROM supply_receipts r JOIN suppliers s ON s.organization_id=r.organization_id AND s.id=r.supplier_id JOIN application_users u ON u.id=r.received_by WHERE r.organization_id=$1 AND r.id=$2`, org, id))
	if e != nil {
		return v, e
	}
	values := []domain.Receipt{v}
	e = s.receiptItems(ctx, org, values)
	return values[0], e
}
func (s *store) CreateReceipt(ctx context.Context, a domain.Actor, org string, v domain.Receipt) (domain.Receipt, error) {
	if !hashPattern.MatchString(v.RequestHash) {
		return v, domain.ErrConflict
	}
	tag, e := s.tx.Exec(ctx, `INSERT INTO supply_receipts(id,organization_id,location_id,supplier_id,received_by,received_at,document_reference,notes,client_operation_id,client_created_at,request_hash,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11,$5,$5) ON CONFLICT DO NOTHING`, v.ID, org, v.LocationID, v.SupplierID, a.ID, v.ReceivedAt, v.DocumentReference, v.Notes, v.ClientOperationID, v.ClientCreatedAt, v.RequestHash)
	if e != nil {
		return v, conflict(e)
	}
	if tag.RowsAffected() == 0 {
		var id, hash, user, operation string
		e = s.tx.QueryRow(ctx, `SELECT id::text,COALESCE(request_hash,''),received_by,client_operation_id::text FROM supply_receipts WHERE organization_id=$1 AND (id=$2 OR client_operation_id=$3) ORDER BY id LIMIT 1 FOR SHARE`, org, v.ID, v.ClientOperationID).Scan(&id, &hash, &user, &operation)
		if errors.Is(e, pgx.ErrNoRows) {
			return v, domain.ErrConflict
		}
		if e != nil {
			return v, e
		}
		if id != v.ID || hash != v.RequestHash || user != a.ID || operation != v.ClientOperationID {
			return v, domain.ErrConflict
		}
		return s.Receipt(ctx, org, id)
	}
	for i, item := range v.Items {
		_, e = s.tx.Exec(ctx, `INSERT INTO supply_receipt_items(id,organization_id,receipt_id,product_id,batch_code,expiration_date,quantity,temperature_celsius,minimum_temperature,maximum_temperature,decision,rejection_reason,packaging_condition,storage_location,label,display_order,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::date,$7::numeric,$8,$9,$10,$11,NULLIF($12,''),$13,NULLIF($14,''),NULLIF($15,''),$16,$17,$17)`, item.ID, org, v.ID, item.ProductID, item.BatchCode, item.ExpirationDate, item.Quantity, item.TemperatureCelsius, item.MinimumTemperature, item.MaximumTemperature, item.Decision, item.RejectionReason, item.PackagingCondition, item.StorageLocation, item.Label, i+1, a.ID)
		if e != nil {
			return v, conflict(e)
		}
		if item.Decision == "ACCEPTED" {
			_, e = s.tx.Exec(ctx, `INSERT INTO storage_lots(id,organization_id,location_id,receipt_item_id,storage_location,label,quantity_available,created_by,updated_by) VALUES($1,$2,$3,$1,$4,NULLIF($5,''),$6::numeric,$7,$7)`, item.ID, org, v.LocationID, item.StorageLocation, item.Label, item.Quantity, a.ID)
			if e != nil {
				return v, conflict(e)
			}
		}
	}
	if e = s.Audit(ctx, a, org, "receipt.created", v.ID); e != nil {
		return v, e
	}
	if e = outbox.Enqueue(ctx, s.tx, outbox.Event{OrganizationID: org, Type: "operations.receipt.recorded.v1", AggregateType: "supply_receipt", AggregateID: v.ID, DedupKey: "receipt:" + v.ID, ActorID: a.ID}); e != nil {
		return v, e
	}
	return s.Receipt(ctx, org, v.ID)
}
func dateWindow(w pagination.Window) (time.Time, error) {
	if e := catalogWindow(w); e != nil {
		return time.Time{}, e
	}
	if w.After.ID == "" {
		return time.Time{}, nil
	}
	v, e := time.Parse(time.RFC3339Nano, w.After.Key)
	if e != nil {
		return time.Time{}, pagination.ErrInvalid
	}
	return v, nil
}
func (s *store) Receipts(ctx context.Context, org, site string, w pagination.Window) ([]domain.Receipt, error) {
	after, e := dateWindow(w)
	if e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+receiptFields+` FROM supply_receipts r JOIN suppliers s ON s.organization_id=r.organization_id AND s.id=r.supplier_id JOIN application_users u ON u.id=r.received_by WHERE r.organization_id=$1 AND r.location_id=$2
 AND (strpos(lower(s.name || ' ' || COALESCE(r.document_reference,'') || ' ' || COALESCE(r.notes,'')),lower($3))>0 OR EXISTS(SELECT 1 FROM supply_receipt_items i JOIN products p ON p.organization_id=i.organization_id AND p.id=i.product_id WHERE i.organization_id=r.organization_id AND i.receipt_id=r.id AND strpos(lower(p.name || ' ' || COALESCE(i.batch_code,'')),lower($3))>0))
 AND ($5='' OR (r.received_at,r.id)<($4,NULLIF($5,'')::uuid)) ORDER BY r.received_at DESC,r.id DESC LIMIT $6`, org, site, w.Query, after, w.After.ID, limit(w))
	if e != nil {
		return nil, e
	}
	out := []domain.Receipt{}
	for rows.Next() {
		v, e := scanReceipt(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	e = s.receiptItems(ctx, org, out)
	return out, e
}
func (s *store) Lots(ctx context.Context, org, site string, w pagination.Window) ([]domain.Lot, error) {
	if e := catalogWindow(w); e != nil {
		return nil, e
	}
	var after time.Time
	var receiptItem string
	if w.After.ID != "" {
		key, tail, ok := strings.Cut(w.After.Key, "|")
		if !ok || !domain.ValidID(tail) {
			return nil, pagination.ErrInvalid
		}
		var e error
		after, e = time.Parse(time.RFC3339Nano, key)
		if e != nil {
			return nil, pagination.ErrInvalid
		}
		receiptItem = tail
	}
	rows, e := s.tx.Query(ctx, `SELECT l.id::text,l.location_id::text,l.receipt_item_id::text,i.product_id::text,p.name,p.unit,COALESCE(i.batch_code,''),COALESCE(i.expiration_date::text,''),r.received_at,l.storage_location,COALESCE(l.label,''),l.quantity_available::text,l.status,COALESCE(i.expiration_date < (CURRENT_TIMESTAMP AT TIME ZONE loc.timezone)::date,false)
 FROM storage_lots l JOIN supply_receipt_items i ON i.organization_id=l.organization_id AND i.id=l.receipt_item_id JOIN supply_receipts r ON r.organization_id=i.organization_id AND r.id=i.receipt_id JOIN products p ON p.organization_id=i.organization_id AND p.id=i.product_id JOIN locations loc ON loc.organization_id=l.organization_id AND loc.id=l.location_id
 WHERE l.organization_id=$1 AND l.location_id=$2 AND strpos(lower(p.name || ' ' || COALESCE(i.batch_code,'') || ' ' || l.storage_location || ' ' || COALESCE(l.label,'')),lower($3))>0
 AND ($6='' OR (r.received_at,l.receipt_item_id,l.id)>($4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid)) ORDER BY r.received_at,l.receipt_item_id,l.id LIMIT $7`, org, site, w.Query, after, receiptItem, w.After.ID, limit(w))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Lot{}
	for rows.Next() {
		var v domain.Lot
		e = rows.Scan(&v.ID, &v.LocationID, &v.ReceiptItemID, &v.ProductID, &v.ProductName, &v.Unit, &v.BatchCode, &v.ExpirationDate, &v.ReceivedAt, &v.StorageLocation, &v.Label, &v.QuantityAvailable, &v.Status, &v.Expired)
		if e != nil {
			return nil, e
		}
		v.ReceivedAt = v.ReceivedAt.UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
