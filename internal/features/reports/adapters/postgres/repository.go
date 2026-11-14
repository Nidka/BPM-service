// Package postgres implementa los puertos de reportes sobre PostgreSQL. Toda consulta filtra
// organization_id obtenido de la membresía autenticada y calcula los días con la zona horaria
// de cada sede (America/Lima si falta o es desconocida).
package postgres

import (
	"context"
	"errors"

	"github.com/Nidka/BPM-service/internal/features/reports/application"
	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository abre transacciones y cumple application.UnitOfWork.
type Repository struct{ pool *pgxpool.Pool }

type store struct{ tx pgx.Tx }

// New crea el repositorio.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// RequireSchema evita servir reportes con un esquema incompatible: exige las tablas y la
// función de V10. No ejecuta migraciones.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT c.verification_code, s.compliance_threshold, d.next_attempt_at, e.client_event_id, bpm_report_timezone('America/Lima') FROM compliance_certificates c CROSS JOIN report_settings s CROSS JOIN weekly_report_deliveries d CROSS JOIN usage_events e LIMIT 0`)
	if err != nil {
		return errors.New("reports migration V10 is required")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var valid bool
	err = r.pool.QueryRow(ctx, `SELECT count(*)=2 FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('usage_events_client_event_uq','compliance_certificates_page_idx')`).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("reports migration V10 is required")
	}
	return nil
}

// Within ejecuta fn en una transacción y la revierte si devuelve error.
func (r *Repository) Within(ctx context.Context, fn func(application.Store) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = fn(&store{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Actor resuelve el rol: SUPER_ADMIN de plataforma o rol de una membresía ACTIVE.
func (s *store) Actor(ctx context.Context, user, org string) (domain.Actor, error) {
	a := domain.Actor{ID: user}
	var platform string
	err := s.tx.QueryRow(ctx, `SELECT u.platform_role FROM application_users u JOIN organizations o ON o.id=$2 AND o.status='ACTIVE' WHERE u.id=$1 AND u.status='ACTIVE'`, user, org).Scan(&platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	if err != nil {
		return a, err
	}
	if platform == "SUPER_ADMIN" {
		a.Role = platform
		return a, nil
	}
	err = s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE'`, org, user).Scan(&a.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	return a, err
}

// Audit guarda actor, acción y entidad; nunca datos del reporte.
func (s *store) Audit(ctx context.Context, org, user, action, entityType, entityID string) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,metadata,created_by,updated_by) VALUES($1,$2,$3,$4,$5,'{}'::jsonb,$2,$2)`, org, user, action, entityType, entityID)
	return err
}

// OrganizationName devuelve el nombre comercial (o la razón social).
func (s *store) OrganizationName(ctx context.Context, org string) (string, error) {
	var name string
	err := s.tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(trade_name,''),legal_name) FROM organizations WHERE id=$1`, org).Scan(&name)
	return name, err
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

var _ application.UnitOfWork = (*Repository)(nil)
var _ application.Store = (*store)(nil)
var _ application.CertificateVerifier = (*Repository)(nil)
