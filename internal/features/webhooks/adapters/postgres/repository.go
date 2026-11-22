// Package postgres guarda los webhooks de cada empresa (V18).
package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/Nidka/BPM-service/internal/features/webhooks/application"
	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ application.Store = (*Repository)(nil)

// RequireSchema falla de forma explícita si falta la migración V18.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT id, secret, event_types, last_delivery_at FROM webhook_endpoints LIMIT 0`)
	if err != nil {
		return errors.New("webhooks migration V18 is required")
	}
	rows.Close()
	return rows.Err()
}

// NewID genera un UUID v4.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (r *Repository) CanManage(ctx context.Context, user, org string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM application_users u JOIN organizations o ON o.id=$2 AND o.status='ACTIVE'
 WHERE u.id=$1 AND u.status='ACTIVE' AND (u.platform_role='SUPER_ADMIN' OR EXISTS(SELECT 1 FROM organization_memberships m WHERE m.organization_id=$2 AND m.user_id=$1 AND m.status='ACTIVE' AND m.role='ADMIN')))`, user, org).Scan(&ok)
	return ok, err
}

const columns = `id::text,organization_id,url,secret,event_types,active,last_status,COALESCE(last_error,''),last_delivery_at,created_at,updated_at`

func scan(row pgx.Row) (domain.Endpoint, error) {
	var e domain.Endpoint
	err := row.Scan(&e.ID, &e.OrganizationID, &e.URL, &e.Secret, &e.EventTypes, &e.Active, &e.LastStatus, &e.LastError, &e.LastDeliveryAt, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, domain.ErrNotFound
	}
	return e, err
}

func (r *Repository) list(ctx context.Context, query string, args ...any) ([]domain.Endpoint, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Endpoint{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) List(ctx context.Context, org string) ([]domain.Endpoint, error) {
	return r.list(ctx, `SELECT `+columns+` FROM webhook_endpoints WHERE organization_id=$1 AND deleted_at IS NULL ORDER BY created_at`, org)
}

func (r *Repository) Get(ctx context.Context, org, id string) (domain.Endpoint, error) {
	if !validUUID(id) {
		return domain.Endpoint{}, domain.ErrNotFound
	}
	return scan(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM webhook_endpoints WHERE organization_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, org, id))
}

func (r *Repository) Create(ctx context.Context, user string, e domain.Endpoint) (domain.Endpoint, error) {
	return scan(r.pool.QueryRow(ctx, `INSERT INTO webhook_endpoints(id,organization_id,url,secret,event_types,active,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING `+columns,
		e.ID, e.OrganizationID, e.URL, e.Secret, e.EventTypes, e.Active, user))
}

func (r *Repository) Update(ctx context.Context, user, org, id string, in domain.Input) (domain.Endpoint, error) {
	if !validUUID(id) {
		return domain.Endpoint{}, domain.ErrNotFound
	}
	return scan(r.pool.QueryRow(ctx, `UPDATE webhook_endpoints SET url=$3,event_types=$4,active=$5,updated_by=$6,updated_at=now() WHERE organization_id=$1 AND id=$2::uuid AND deleted_at IS NULL RETURNING `+columns,
		org, id, in.URL, in.EventTypes, in.Active, user))
}

func (r *Repository) SetSecret(ctx context.Context, user, org, id, secret string) (domain.Endpoint, error) {
	if !validUUID(id) {
		return domain.Endpoint{}, domain.ErrNotFound
	}
	return scan(r.pool.QueryRow(ctx, `UPDATE webhook_endpoints SET secret=$3,updated_by=$4,updated_at=now() WHERE organization_id=$1 AND id=$2::uuid AND deleted_at IS NULL RETURNING `+columns,
		org, id, secret, user))
}

func (r *Repository) Delete(ctx context.Context, user, org, id string) error {
	if !validUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := r.pool.Exec(ctx, `UPDATE webhook_endpoints SET deleted_at=now(),active=false,updated_by=$3,updated_at=now() WHERE organization_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, org, id, user)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (r *Repository) Subscribed(ctx context.Context, org, eventType string) ([]domain.Endpoint, error) {
	return r.list(ctx, `SELECT `+columns+` FROM webhook_endpoints WHERE organization_id=$1 AND deleted_at IS NULL AND active AND $2=ANY(event_types) ORDER BY created_at`, org, eventType)
}

func (r *Repository) RecordResult(ctx context.Context, id string, status int, failure string, at time.Time) error {
	var code *int
	if status > 0 {
		code = &status
	}
	_, err := r.pool.Exec(ctx, `UPDATE webhook_endpoints SET last_status=$2,last_error=NULLIF($3,''),last_delivery_at=$4 WHERE id=$1::uuid`, id, code, failure, at)
	return err
}

func validUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'):
		default:
			return false
		}
	}
	return true
}
