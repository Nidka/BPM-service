// Package postgres persiste los ajustes de plataforma (V22).
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Nidka/BPM-service/internal/features/platformsettings/application"
	"github.com/Nidka/BPM-service/internal/features/platformsettings/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ application.Store = (*Repository)(nil)

// RequireSchema falla de forma explícita si falta la migración V22.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT s.support_email, s.support_whatsapp, u.onboarding_seen_at FROM platform_settings s CROSS JOIN application_users u LIMIT 0`)
	if err != nil {
		return errors.New("platform settings migration V22 is required")
	}
	rows.Close()
	return rows.Err()
}

func (r *Repository) PlatformRole(ctx context.Context, user string) (string, error) {
	var role string
	err := r.pool.QueryRow(ctx, `SELECT platform_role FROM application_users WHERE id=$1 AND status='ACTIVE'`, user).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrDenied
	}
	if err != nil {
		return "", fmt.Errorf("platform role: %w", err)
	}
	return role, nil
}

func (r *Repository) SupportContact(ctx context.Context) (domain.SupportContact, error) {
	var c domain.SupportContact
	err := r.pool.QueryRow(ctx, `SELECT support_email, COALESCE(support_whatsapp,''), updated_at FROM platform_settings WHERE id=1`).Scan(&c.Email, &c.WhatsApp, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Fila ausente (no debería ocurrir tras V22): se responde el valor por defecto.
		return domain.SupportContact{Email: domain.DefaultSupportEmail}, nil
	}
	if err != nil {
		return c, fmt.Errorf("support contact: %w", err)
	}
	return c, nil
}

func (r *Repository) SaveSupportContact(ctx context.Context, user string, c domain.SupportContact) (domain.SupportContact, error) {
	var out domain.SupportContact
	err := r.pool.QueryRow(ctx, `INSERT INTO platform_settings(id,support_email,support_whatsapp,updated_at,updated_by)
 VALUES(1,$1,NULLIF($2,''),now(),$3)
 ON CONFLICT(id) DO UPDATE SET support_email=EXCLUDED.support_email, support_whatsapp=EXCLUDED.support_whatsapp, updated_at=now(), updated_by=EXCLUDED.updated_by
 RETURNING support_email, COALESCE(support_whatsapp,''), updated_at`, c.Email, c.WhatsApp, user).Scan(&out.Email, &out.WhatsApp, &out.UpdatedAt)
	if err != nil {
		return out, fmt.Errorf("save support contact: %w", err)
	}
	return out, nil
}
