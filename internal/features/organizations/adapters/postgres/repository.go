package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }
type store struct{ tx pgx.Tx }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }
func (r *Repository) Within(ctx context.Context, fn func(application.Store) error) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = fn(&store{tx: tx}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *store) Actor(ctx context.Context, uid string) (a domain.Actor, e error) {
	e = s.tx.QueryRow(ctx, `SELECT id,platform_role FROM application_users WHERE id=$1 AND status='ACTIVE' FOR SHARE`, uid).Scan(&a.ID, &a.Role)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrDenied
	}
	return
}

const orgColumns = `o.id,o.legal_name,COALESCE(o.trade_name,''),COALESCE(o.tax_id,''),o.business_type,COALESCE(o.contact_name,''),COALESCE(o.contact_phone,''),COALESCE(o.contact_email,'')`

func (s *store) Organizations(ctx context.Context, a domain.Actor) ([]domain.Organization, error) {
	rows, e := s.tx.Query(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.status='ACTIVE' AND ($2='SUPER_ADMIN' OR EXISTS(SELECT 1 FROM organization_memberships m WHERE m.organization_id=o.id AND m.user_id=$1 AND m.status='ACTIVE')) ORDER BY o.legal_name,o.id LIMIT 100`, a.ID, a.Role)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []domain.Organization{}
	for rows.Next() {
		var o domain.Organization
		if e = rows.Scan(&o.ID, &o.LegalName, &o.TradeName, &o.TaxID, &o.BusinessType, &o.ContactName, &o.ContactPhone, &o.ContactEmail); e != nil {
			return nil, e
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
func (s *store) Role(ctx context.Context, a domain.Actor, org string) (string, error) {
	var active string
	if e := s.tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 AND status='ACTIVE' FOR SHARE`, org).Scan(&active); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return "", domain.ErrDenied
		}
		return "", e
	}
	if a.Role == "SUPER_ADMIN" {
		return a.Role, nil
	}
	var role string
	e := s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' FOR SHARE`, org, a.ID).Scan(&role)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrDenied
	}
	return role, e
}
func (s *store) Workspace(ctx context.Context, a domain.Actor, org, role string) (domain.Workspace, error) {
	return s.WorkspacePage(ctx, a, org, role, 100)
}

func (s *store) entries(ctx context.Context, q string, args ...any) ([]domain.Entry, error) {
	rows, e := s.tx.Query(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Entry{}
	for rows.Next() {
		var v domain.Entry
		if e = rows.Scan(&v.ID, &v.Title, &v.Detail, &v.Status, &v.CursorKey); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) audit(ctx context.Context, a domain.Actor, org, action, kind, id string) error {
	_, e := s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$2,$2)`, org, a.ID, action, kind, id)
	return e
}
func conflict(e error) error {
	var p *pgconn.PgError
	if errors.As(e, &p) && p.Code == "23505" {
		return domain.ErrConflict
	}
	return e
}
func (s *store) CreateOrganization(ctx context.Context, a domain.Actor, o domain.Organization) error {
	tag, e := s.tx.Exec(ctx, `INSERT INTO organizations(id,legal_name,trade_name,tax_id,business_type,contact_name,contact_phone,contact_email,created_by,updated_by) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$9) ON CONFLICT(id) DO NOTHING`, o.ID, o.LegalName, o.TradeName, o.TaxID, o.BusinessType, o.ContactName, o.ContactPhone, o.ContactEmail, a.ID)
	if e != nil {
		return conflict(e)
	}
	if tag.RowsAffected() == 0 {
		var same bool
		e = s.tx.QueryRow(ctx, `SELECT legal_name=$2 AND COALESCE(trade_name,'')=$3 AND COALESCE(tax_id,'')=$4 AND business_type=$5 AND COALESCE(contact_name,'')=$6 AND COALESCE(contact_phone,'')=$7 AND COALESCE(contact_email,'')=$8 AND created_by=$9 FROM organizations WHERE id=$1`, o.ID, o.LegalName, o.TradeName, o.TaxID, o.BusinessType, o.ContactName, o.ContactPhone, o.ContactEmail, a.ID).Scan(&same)
		if e != nil {
			return e
		}
		if !same {
			return domain.ErrConflict
		}
		return nil
	}
	return s.audit(ctx, a, o.ID, "organization.created", "organization", o.ID)
}
func (s *store) CreateLocation(ctx context.Context, a domain.Actor, org string, l domain.Location) error {
	tag, e := s.tx.Exec(ctx, `INSERT INTO locations(id,organization_id,name,address,district,contact_phone,timezone,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8) ON CONFLICT(id) DO NOTHING`, l.ID, org, l.Name, l.Address, l.District, l.ContactPhone, l.Timezone, a.ID)
	if e != nil {
		return conflict(e)
	}
	if tag.RowsAffected() == 0 {
		var same bool
		e = s.tx.QueryRow(ctx, `SELECT organization_id=$2 AND name=$3 AND address=$4 AND COALESCE(district,'')=$5 AND COALESCE(contact_phone,'')=$6 AND created_by=$7 FROM locations WHERE id=$1`, l.ID, org, l.Name, l.Address, l.District, l.ContactPhone, a.ID).Scan(&same)
		if e != nil {
			return e
		}
		if !same {
			return domain.ErrConflict
		}
		return nil
	}
	return s.audit(ctx, a, org, "location.created", "location", l.ID)
}
func (s *store) AddMember(ctx context.Context, a domain.Actor, org, email, role string, locations []string) error {
	var uid string
	e := s.tx.QueryRow(ctx, `SELECT id FROM application_users WHERE LOWER(email)=LOWER($1) AND status='ACTIVE' FOR SHARE`, email).Scan(&uid)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrMemberUnavailable
	}
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, id := range locations {
		if seen[id] {
			continue
		}
		seen[id] = true
		var found string
		e = s.tx.QueryRow(ctx, `SELECT id::text FROM locations WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR SHARE`, org, id).Scan(&found)
		if errors.Is(e, pgx.ErrNoRows) {
			return &domain.ValidationError{Fields: map[string]string{"locationIds": "Una sede no pertenece a esta organización."}}
		}
		if e != nil {
			return e
		}
	}
	_, e = s.tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,created_by,updated_by) VALUES($1,$2,$3,$4,$4)`, org, uid, role, a.ID)
	if e != nil {
		return conflict(e)
	}
	for id := range seen {
		_, e = s.tx.Exec(ctx, `INSERT INTO membership_locations(organization_id,user_id,location_id,created_by,updated_by) VALUES($1,$2,$3,$4,$4)`, org, uid, id, a.ID)
		if e != nil {
			return e
		}
	}
	return s.audit(ctx, a, org, "membership.created", "membership", uid)
}
