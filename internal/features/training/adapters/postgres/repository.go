// Package postgres implementa los puertos de la Academia BPM sobre PostgreSQL.
// Toda consulta filtra organization_id obtenido de la membresía autenticada.
package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository abre transacciones y cumple application.UnitOfWork.
type Repository struct{ pool *pgxpool.Pool }

type store struct{ tx pgx.Tx }

// New crea el repositorio.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// RequireSchema evita servir la Academia con un esquema incompatible: exige las
// columnas y unicidades de V8, V11, V15, V16 y V19. No ejecuta migraciones.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT v.row_version, a.request_hash FROM training_course_versions v CROSS JOIN training_attempts a LIMIT 0`)
	if err != nil {
		return errors.New("training migration V8 is required")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var valid bool
	err = r.pool.QueryRow(ctx, `SELECT count(*)=3 FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('training_versions_one_draft_uq','training_versions_one_published_uq','training_certificates_user_version_uq')`).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("training migration V8 is required")
	}
	// V11: asignaciones, rutas, insignias, módulos y video externo.
	rows, err = r.pool.Query(ctx, `SELECT l.external_video_id, x.cancelled_at, t.lesson_id, m.best_score, a.criterion_type, u.source_attempt_id, p.row_version, pc.position
 FROM training_lessons l CROSS JOIN training_assignments x CROSS JOIN training_attempts t CROSS JOIN training_module_results m
 CROSS JOIN training_achievements a CROSS JOIN user_achievements u CROSS JOIN learning_paths p CROSS JOIN learning_path_courses pc LIMIT 0`)
	if err != nil {
		return errors.New("training migration V11 is required")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	err = r.pool.QueryRow(ctx, `SELECT count(*)=1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='training_assignments_active_uq'`).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("training migration V11 is required")
	}
	// V15: progreso por lección, puntos clave, XP por intento e índice de limpieza.
	rows, err = r.pool.Query(ctx, `SELECT lp.lesson_id, lp.source, lp.completed_at, l.key_points, t.xp_awarded
 FROM training_lesson_progress lp CROSS JOIN training_lessons l CROSS JOIN training_attempts t LIMIT 0`)
	if err != nil {
		return errors.New("training migration V15 is required")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	err = r.pool.QueryRow(ctx, `SELECT count(*)=1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='cleaning_executions_plan_idx'`).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("training migration V15 is required")
	}
	// V16: subtítulos WebVTT por lección (RF-45).
	rows, err = r.pool.Query(ctx, `SELECT l.captions_vtt FROM training_lessons l LIMIT 0`)
	if err != nil {
		return errors.New("training migration V16 is required")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// V19: banco oficial de preguntas (metadatos del ítem, sorteo de evaluaciones y práctica).
	rows, err = r.pool.Query(ctx, `SELECT q.item_code, q.bank_version, q.bloom_level, q.source_refs, a.item_code, a.bank_version, a.option_order, d.items, p.box
 FROM training_questions q CROSS JOIN training_attempt_answers a CROSS JOIN training_evaluation_draws d CROSS JOIN training_practice_results p LIMIT 0`)
	if err != nil {
		return errors.New("training migration V19 is required")
	}
	rows.Close()
	return rows.Err()
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
	err := s.tx.QueryRow(ctx, `SELECT u.platform_role FROM application_users u JOIN organizations o ON o.id=$2 AND o.status='ACTIVE' WHERE u.id=$1 AND u.status='ACTIVE' FOR SHARE OF u,o`, user, org).Scan(&platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	if err != nil {
		return a, err
	}
	// Un SUPER_ADMIN con membresía activa opera con el rol de su membresía.
	err = s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' FOR SHARE`, org, user).Scan(&a.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		if platform == "SUPER_ADMIN" {
			a.Role = platform
			return a, nil
		}
		return a, domain.ErrDenied
	}
	return a, err
}

// Audit guarda actor, acción y entidad; nunca respuestas, contenido ni URL.
func (s *store) Audit(ctx context.Context, org, user, action, entityType, entityID string) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,metadata,created_by,updated_by) VALUES($1,$2,$3,$4,$5,'{}'::jsonb,$2,$2)`, org, user, action, entityType, entityID)
	return err
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// trainingVideoPrefix es la clave privada de los videos de una organización.
func trainingVideoPrefix(org string) string {
	return "organizations/" + org + "/training/videos/"
}

// newUUID genera un UUID v4 con crypto/rand para copiar contenido publicado en un borrador.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

var _ application.UnitOfWork = (*Repository)(nil)
var _ application.Store = (*store)(nil)
