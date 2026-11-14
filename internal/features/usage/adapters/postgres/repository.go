// Package postgres implementa los puertos del registro de uso. Nunca guarda metadatos del cliente.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/usage/application"
	"github.com/Nidka/BPM-service/internal/features/usage/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository abre transacciones y cumple application.UnitOfWork.
type Repository struct{ pool *pgxpool.Pool }

type store struct{ tx pgx.Tx }

// New crea el repositorio.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// RequireSchema exige la migración V10 (columna e índice de idempotencia de eventos).
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT client_event_id FROM usage_events LIMIT 0`)
	if err != nil {
		return errors.New("usage migration V10 is required")
	}
	rows.Close()
	var ok bool
	if err = r.pool.QueryRow(ctx, `SELECT count(*)=1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='usage_events_client_event_uq'`).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errors.New("usage migration V10 is required")
	}
	return nil
}

// Within ejecuta fn en una transacción.
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
	// Un SUPER_ADMIN con membresía registra como su rol de membresía; sin ella solo consulta.
	err = s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE'`, org, user).Scan(&a.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		if platform == "SUPER_ADMIN" {
			a.Role = platform
			return a, nil
		}
		return a, domain.ErrDenied
	}
	return a, err
}

// EnsureSession crea la sesión (idempotente) y verifica que sea del mismo usuario y organización.
func (s *store) EnsureSession(ctx context.Context, org, user, session string, startedAt time.Time) error {
	if _, err := s.tx.Exec(ctx, `INSERT INTO usage_sessions(id,organization_id,user_id,started_at,created_by,updated_by) VALUES($1::uuid,$2,$3,$4,$3,$3) ON CONFLICT (id) DO NOTHING`, session, org, user, startedAt); err != nil {
		return err
	}
	var owner string
	err := s.tx.QueryRow(ctx, `SELECT user_id FROM usage_sessions WHERE organization_id=$1 AND id=$2::uuid`, org, session).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != user) {
		return domain.ErrSessionConflict
	}
	return err
}

// InsertEvent guarda el evento con metadata vacía; false si el UUID ya existía.
func (s *store) InsertEvent(ctx context.Context, org, user string, e domain.Event) (bool, error) {
	tag, err := s.tx.Exec(ctx, `INSERT INTO usage_events(organization_id,session_id,user_id,module,event_name,occurred_at,duration_seconds,metadata,client_event_id,created_by,updated_by)
 VALUES($1,$2::uuid,$3,$4,$5,$6,$7,'{}'::jsonb,$8::uuid,$3,$3) ON CONFLICT (organization_id,client_event_id) WHERE client_event_id IS NOT NULL DO NOTHING`,
		org, e.SessionID, user, e.Module, e.Type, e.OccurredAt, e.DurationSeconds, e.ID)
	return tag.RowsAffected() == 1, err
}

// FindEvent devuelve el evento guardado y su usuario.
func (s *store) FindEvent(ctx context.Context, org, id string) (domain.Event, string, error) {
	var e domain.Event
	var user string
	err := s.tx.QueryRow(ctx, `SELECT client_event_id::text, session_id::text, user_id, module, event_name, occurred_at, duration_seconds FROM usage_events WHERE organization_id=$1 AND client_event_id=$2::uuid`, org, id).
		Scan(&e.ID, &e.SessionID, &user, &e.Module, &e.Type, &e.OccurredAt, &e.DurationSeconds)
	e.OccurredAt = e.OccurredAt.UTC()
	return e, user, err
}

// MarkSessionStart adelanta el inicio si el evento es anterior.
func (s *store) MarkSessionStart(ctx context.Context, org, session string, at time.Time) error {
	_, err := s.tx.Exec(ctx, `UPDATE usage_sessions SET started_at=LEAST(started_at,$3), updated_at=NOW() WHERE organization_id=$1 AND id=$2::uuid`, org, session, at)
	return err
}

// MarkSessionEnd fija el fin de sesión sin que sea anterior al inicio.
func (s *store) MarkSessionEnd(ctx context.Context, org, session string, at time.Time) error {
	_, err := s.tx.Exec(ctx, `UPDATE usage_sessions SET ended_at=GREATEST($3,started_at,COALESCE(ended_at,$3)), updated_at=NOW() WHERE organization_id=$1 AND id=$2::uuid`, org, session, at)
	return err
}

// Summary agrega sesiones y eventos del período (días en hora de Lima) de todos los usuarios; sin datos de personas.
func (s *store) Summary(ctx context.Context, org string, p domain.Period) (domain.Summary, error) {
	return s.summary(ctx, org, p, "")
}

// ResearchSummary agrega solo el uso de usuarios con participación ACTIVE y consentimiento de
// investigación vigente (último registro aceptado sobre el documento ACTIVE).
func (s *store) ResearchSummary(ctx context.Context, org string, p domain.Period) (domain.Summary, error) {
	return s.summary(ctx, org, p, "researchOnly")
}

// consentFilter devuelve la condición SQL que limita las filas de la tabla t a usuarios con
// consentimiento de investigación vigente; las tablas son las del módulo research.
func consentFilter(t string) string {
	return ` AND EXISTS (SELECT 1 FROM research_participants rp WHERE rp.organization_id=` + t + `.organization_id AND rp.user_id=` + t + `.user_id AND rp.status='ACTIVE'
  AND EXISTS (SELECT 1 FROM consent_records c WHERE c.organization_id=rp.organization_id AND c.participant_id=rp.id AND c.accepted
    AND c.document_version=(SELECT d.version FROM research_consent_documents d WHERE d.status='ACTIVE')
    AND c.id=(SELECT c2.id FROM consent_records c2 WHERE c2.organization_id=rp.organization_id AND c2.participant_id=rp.id ORDER BY c2.accepted_at DESC,c2.created_at DESC,c2.id DESC LIMIT 1)))`
}

func (s *store) summary(ctx context.Context, org string, p domain.Period, scope string) (domain.Summary, error) {
	filter := func(t string) string {
		if scope == "researchOnly" {
			return consentFilter(t)
		}
		return ""
	}
	from, to := p.Bounds()
	out := domain.Summary{From: p.From.Format(domain.DateLayout), To: p.To.Format(domain.DateLayout), Modules: []domain.ModuleStat{}, Days: []domain.DayStat{}}
	err := s.tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE ended_at IS NOT NULL), count(DISTINCT user_id),
  round(avg(LEAST(EXTRACT(EPOCH FROM (ended_at-started_at)),86400)) FILTER (WHERE ended_at IS NOT NULL)::numeric,2)::float8
 FROM usage_sessions WHERE organization_id=$1 AND started_at>=$2 AND started_at<$3`+filter("usage_sessions"), org, from, to).
		Scan(&out.Sessions, &out.EndedSessions, &out.ActiveUsers, &out.AverageSessionSeconds)
	if err != nil {
		return out, err
	}
	err = s.tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_name='ALERT_VIEWED'), count(*) FILTER (WHERE event_name='ALERT_ATTENDED')
 FROM usage_events WHERE organization_id=$1 AND occurred_at>=$2 AND occurred_at<$3`+filter("usage_events"), org, from, to).Scan(&out.AlertsViewed, &out.AlertsAttended)
	if err != nil {
		return out, err
	}
	rows, err := s.tx.Query(ctx, `SELECT module, count(*), count(DISTINCT user_id), COALESCE(sum(duration_seconds),0)
 FROM usage_events WHERE organization_id=$1 AND event_name='MODULE_VISIT' AND occurred_at>=$2 AND occurred_at<$3`+filter("usage_events")+` GROUP BY module ORDER BY module`, org, from, to)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m domain.ModuleStat
		if err = rows.Scan(&m.Module, &m.Visits, &m.UniqueUsers, &m.TotalSeconds); err != nil {
			rows.Close()
			return out, err
		}
		out.Modules = append(out.Modules, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.tx.Query(ctx, `SELECT (started_at AT TIME ZONE 'America/Lima')::date, count(*) FROM usage_sessions
 WHERE organization_id=$1 AND started_at>=$2 AND started_at<$3`+filter("usage_sessions")+` GROUP BY 1 ORDER BY 1`, org, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d time.Time
		var n int
		if err = rows.Scan(&d, &n); err != nil {
			return out, err
		}
		out.Days = append(out.Days, domain.DayStat{Date: d.Format(domain.DateLayout), Sessions: n})
	}
	return out, rows.Err()
}

var _ application.UnitOfWork = (*Repository)(nil)
var _ application.Store = (*store)(nil)
