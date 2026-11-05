package postgres

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"reflect"
)

const supplierFields = `id::text,name,COALESCE(tax_id,''),COALESCE(contact_name,''),COALESCE(contact_phone,''),COALESCE(contact_email,''),COALESCE(address,'')`

func scanSupplier(row pgx.Row) (v domain.Supplier, e error) {
	e = row.Scan(&v.ID, &v.Name, &v.TaxID, &v.ContactName, &v.ContactPhone, &v.ContactEmail, &v.Address)
	return v, missing(e)
}
func (s *store) Supplier(ctx context.Context, org, id string) (domain.Supplier, error) {
	return scanSupplier(s.tx.QueryRow(ctx, `SELECT `+supplierFields+` FROM suppliers WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR SHARE`, org, id))
}

const productFields = `id::text,name,COALESCE(category,''),unit,requires_temperature,minimum_temperature::double precision,maximum_temperature::double precision,row_version`

func scanProduct(row pgx.Row) (v domain.Product, e error) {
	e = row.Scan(&v.ID, &v.Name, &v.Category, &v.Unit, &v.RequiresTemperature, &v.MinimumTemperature, &v.MaximumTemperature, &v.RowVersion)
	return v, missing(e)
}
func (s *store) Product(ctx context.Context, org, id string) (domain.Product, error) {
	return scanProduct(s.tx.QueryRow(ctx, `SELECT `+productFields+` FROM products WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR SHARE`, org, id))
}
func (s *store) CreateSupplier(ctx context.Context, a domain.Actor, org string, v domain.Supplier) (domain.Supplier, error) {
	tag, e := s.tx.Exec(ctx, `INSERT INTO suppliers(id,organization_id,name,tax_id,contact_name,contact_phone,contact_email,address,created_by,updated_by) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,$9) ON CONFLICT DO NOTHING`, v.ID, org, v.Name, v.TaxID, v.ContactName, v.ContactPhone, v.ContactEmail, v.Address, a.ID)
	if e != nil {
		return v, conflict(e)
	}
	saved, e := s.Supplier(ctx, org, v.ID)
	if e != nil {
		if e == domain.ErrNotFound {
			return v, domain.ErrConflict
		}
		return v, e
	}
	if tag.RowsAffected() == 0 {
		var creator string
		e = s.tx.QueryRow(ctx, `SELECT COALESCE(created_by,'') FROM suppliers WHERE organization_id=$1 AND id=$2`, org, v.ID).Scan(&creator)
		if e != nil {
			return v, e
		}
		if creator != a.ID || !reflect.DeepEqual(saved, v) {
			return v, domain.ErrConflict
		}
		return saved, nil
	}
	return saved, s.Audit(ctx, a, org, "supplier.created", v.ID)
}
func (s *store) CreateProduct(ctx context.Context, a domain.Actor, org string, v domain.Product) (domain.Product, error) {
	tag, e := s.tx.Exec(ctx, `INSERT INTO products(id,organization_id,name,category,unit,requires_temperature,minimum_temperature,maximum_temperature,created_by,updated_by) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$9) ON CONFLICT DO NOTHING`, v.ID, org, v.Name, v.Category, v.Unit, v.RequiresTemperature, v.MinimumTemperature, v.MaximumTemperature, a.ID)
	if e != nil {
		return v, conflict(e)
	}
	saved, e := s.Product(ctx, org, v.ID)
	if e != nil {
		if e == domain.ErrNotFound {
			return v, domain.ErrConflict
		}
		return v, e
	}
	if tag.RowsAffected() == 0 {
		var creator string
		e = s.tx.QueryRow(ctx, `SELECT COALESCE(created_by,'') FROM products WHERE organization_id=$1 AND id=$2`, org, v.ID).Scan(&creator)
		if e != nil {
			return v, e
		}
		if creator != a.ID || !reflect.DeepEqual(saved, v) {
			return v, domain.ErrConflict
		}
		return saved, nil
	}
	return saved, s.Audit(ctx, a, org, "product.created", v.ID)
}
func (s *store) UpdatePolicy(ctx context.Context, a domain.Actor, org, id string, v domain.TemperaturePolicy) (domain.Product, error) {
	tag, e := s.tx.Exec(ctx, `UPDATE products SET requires_temperature=$3,minimum_temperature=$4,maximum_temperature=$5,row_version=row_version+1,updated_by=$6 WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' AND row_version=$7`, org, id, v.RequiresTemperature, v.MinimumTemperature, v.MaximumTemperature, a.ID, v.RowVersion)
	if e != nil {
		return domain.Product{}, conflict(e)
	}
	if tag.RowsAffected() != 1 {
		_, e = s.Product(ctx, org, id)
		if e != nil {
			return domain.Product{}, e
		}
		return domain.Product{}, domain.ErrConflict
	}
	if e = s.Audit(ctx, a, org, "product.temperature_policy.updated", id); e != nil {
		return domain.Product{}, e
	}
	return s.Product(ctx, org, id)
}
func catalogWindow(w pagination.Window) error {
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return pagination.ErrInvalid
	}
	return nil
}
func limit(w pagination.Window) int {
	if w.Size == 0 {
		return pagination.DefaultSize + 1
	}
	return w.Size + 1
}
func (s *store) Suppliers(ctx context.Context, org string, w pagination.Window) ([]domain.Supplier, error) {
	if e := catalogWindow(w); e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+supplierFields+` FROM suppliers WHERE organization_id=$1 AND status='ACTIVE' AND strpos(lower(name || ' ' || COALESCE(tax_id,'')),lower($2))>0 AND ($4='' OR (lower(name) COLLATE "C",id)>($3 COLLATE "C",NULLIF($4,'')::uuid)) ORDER BY lower(name) COLLATE "C",id LIMIT $5`, org, w.Query, w.After.Key, w.After.ID, limit(w))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Supplier{}
	for rows.Next() {
		v, e := scanSupplier(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) Products(ctx context.Context, org string, w pagination.Window) ([]domain.Product, error) {
	if e := catalogWindow(w); e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+productFields+` FROM products WHERE organization_id=$1 AND status='ACTIVE' AND strpos(lower(name || ' ' || COALESCE(category,'')),lower($2))>0 AND ($4='' OR (lower(name) COLLATE "C",id)>($3 COLLATE "C",NULLIF($4,'')::uuid)) ORDER BY lower(name) COLLATE "C",id LIMIT $5`, org, w.Query, w.After.Key, w.After.ID, limit(w))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Product{}
	for rows.Next() {
		v, e := scanProduct(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
