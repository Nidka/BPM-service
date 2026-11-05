package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

type Repository struct{ pool *pgxpool.Pool }
type store struct {
	tx    pgx.Tx
	actor domain.Actor
}

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// RequireSchema rechaza arrancar si faltan columnas o la unicidad transaccional de V5.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, e := r.pool.Query(ctx, `SELECT p.minimum_temperature,p.maximum_temperature,p.row_version,i.packaging_condition,i.minimum_temperature,i.maximum_temperature,i.storage_location,i.label,i.display_order,r.request_hash,e.request_hash
 FROM products p CROSS JOIN supply_receipt_items i CROSS JOIN supply_receipts r CROSS JOIN cleaning_executions e LIMIT 0`)
	if e != nil {
		return errors.New("daily operations migration V5 is required")
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	var valid bool
	e = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index x JOIN pg_class c ON c.oid=x.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='storage_lots_receipt_item_uq' AND n.nspname=current_schema() AND x.indisunique AND x.indisvalid)`).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return errors.New("daily operations migration V5 is required")
	}
	return nil
}
func (r *Repository) Within(ctx context.Context, fn func(application.Store) error) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = fn(&store{tx: tx}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func conflict(e error) error {
	var pg *pgconn.PgError
	if errors.As(e, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
		return domain.ErrConflict
	}
	return e
}
func missing(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return e
}
func (s *store) Actor(ctx context.Context, user, org string) (domain.Actor, error) {
	a := domain.Actor{ID: user}
	var platform string
	e := s.tx.QueryRow(ctx, `SELECT u.platform_role FROM application_users u JOIN organizations o ON o.id=$2 AND o.status='ACTIVE' WHERE u.id=$1 AND u.status='ACTIVE' FOR SHARE OF u,o`, user, org).Scan(&platform)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	if e != nil {
		return a, e
	}
	if platform == "SUPER_ADMIN" {
		a.Role = platform
		s.actor = a
		return a, nil
	}
	e = s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' FOR SHARE`, org, user).Scan(&a.Role)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrDenied
	}
	if e == nil {
		s.actor = a
	}
	return a, e
}
func (s *store) Location(ctx context.Context, a domain.Actor, org, id string) (string, error) {
	actor, e := s.Actor(ctx, a.ID, org)
	if e != nil {
		return "", e
	}
	var zone string
	e = s.tx.QueryRow(ctx, `SELECT timezone FROM locations WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR SHARE`, org, id).Scan(&zone)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if e != nil {
		return "", e
	}
	if actor.Role != "ADMIN" && actor.Role != "SUPER_ADMIN" {
		var found string
		e = s.tx.QueryRow(ctx, `SELECT location_id::text FROM membership_locations WHERE organization_id=$1 AND user_id=$2 AND location_id=$3 FOR SHARE`, org, actor.ID, id).Scan(&found)
		if errors.Is(e, pgx.ErrNoRows) {
			return "", domain.ErrDenied
		}
	}
	return zone, e
}
func (s *store) Lock(ctx context.Context, org string) error {
	_, e := s.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('operations:' || $1,0))`, org)
	return e
}
func (s *store) Replay(ctx context.Context, org, key, hash string) ([]byte, bool, error) {
	var saved, actor string
	var body []byte
	e := s.tx.QueryRow(ctx, `SELECT request_hash,response_body,COALESCE(created_by,'') FROM idempotency_records WHERE organization_id=$1 AND idempotency_key=$2 FOR SHARE`, org, key).Scan(&saved, &body, &actor)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	if saved != hash || len(body) == 0 || actor != s.actor.ID {
		return nil, false, domain.ErrConflict
	}
	return body, true, nil
}
func (s *store) Remember(ctx context.Context, a domain.Actor, org, key, hash string, body []byte) error {
	if a.ID != s.actor.ID {
		return domain.ErrDenied
	}
	_, e := s.tx.Exec(ctx, `INSERT INTO idempotency_records(organization_id,idempotency_key,request_hash,response_status,response_body,expires_at,created_by,updated_by) VALUES($1,$2,$3,200,$4,NOW()+INTERVAL '24 hours',$5,$5)`, org, key, hash, body, a.ID)
	return conflict(e)
}

// Audit guarda identidad y acción; nunca notas, mediciones ni cuerpos del cliente.
func (s *store) Audit(ctx context.Context, a domain.Actor, org, action, id string) error {
	kind, _, _ := strings.Cut(action, ".")
	_, e := s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,metadata,created_by,updated_by) VALUES($1,$2,$3,$4,$5,'{}'::jsonb,$2,$2)`, org, a.ID, action, kind, id)
	return e
}

var _ application.UnitOfWork = (*Repository)(nil)
var _ application.Store = (*store)(nil)
