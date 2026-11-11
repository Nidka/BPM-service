// Package postgres implementa los puertos del módulo de investigación sobre PostgreSQL.
// El acceso de investigación se decide por la concesión de plataforma; los datos
// del participante siempre se filtran por organization_id de su membresía.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository abre transacciones y cumple application.UnitOfWork.
type Repository struct{ pool *pgxpool.Pool }

type store struct{ tx pgx.Tx }

// New crea el repositorio.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// RequireSchema evita servir el módulo con un esquema incompatible: exige las
// tablas, columnas e índices de V9. No ejecuta migraciones.
func (r *Repository) RequireSchema(ctx context.Context) error {
	missing := errors.New("research migration V9 is required")
	rows, err := r.pool.Query(ctx, `SELECT i.is_cap, v.row_version, q.correct_option, s.request_hash, s.knowledge_score, a.is_correct, c.responsible_investigator,
		d.version, g.user_id, n.id FROM survey_instruments i CROSS JOIN survey_instrument_versions v CROSS JOIN survey_questions q CROSS JOIN survey_submissions s
		CROSS JOIN survey_answers a CROSS JOIN consent_records c CROSS JOIN research_consent_documents d CROSS JOIN research_access_grants g CROSS JOIN research_company_needs n LIMIT 0`)
	if err != nil {
		return missing
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var count int
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN
		('survey_instruments_one_cap_uq','survey_versions_one_draft_uq','survey_versions_one_published_uq','research_participants_user_uq','research_participants_code_uq','research_consent_documents_one_active_uq')`).Scan(&count)
	if err != nil {
		return err
	}
	if count != 6 {
		return missing
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

func uniqueViolation(err error) (constraint string, ok bool) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return pg.ConstraintName, true
	}
	return "", false
}

func exists(ctx context.Context, tx pgx.Tx, sql string, args ...any) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "SELECT EXISTS("+sql+")", args...).Scan(&ok)
	return ok, err
}

// ---- Autorización y auditoría ----

func (s *store) IsResearcher(ctx context.Context, user string) (bool, error) {
	return exists(ctx, s.tx, `SELECT 1 FROM research_access_grants g JOIN application_users u ON u.id=g.user_id WHERE g.user_id=$1 AND g.revoked_at IS NULL AND u.status='ACTIVE'`, user)
}

func (s *store) IsSuperAdmin(ctx context.Context, user string) (bool, error) {
	return exists(ctx, s.tx, `SELECT 1 FROM application_users WHERE id=$1 AND platform_role='SUPER_ADMIN' AND status='ACTIVE'`, user)
}

func (s *store) IsMember(ctx context.Context, user, org string) (bool, error) {
	return exists(ctx, s.tx, `SELECT 1 FROM organization_memberships m JOIN application_users u ON u.id=m.user_id JOIN organizations o ON o.id=m.organization_id
		WHERE m.organization_id=$1 AND m.user_id=$2 AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE'`, org, user)
}

// Audit guarda actor, acción y entidad; los metadatos nunca contienen respuestas ni identidad.
func (s *store) Audit(ctx context.Context, org, user, action, entityType, entityID string, metadata map[string]string) error {
	if metadata == nil {
		metadata = map[string]string{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,metadata,created_by,updated_by)
		VALUES(NULLIF($1,''),$2,$3,$4,NULLIF($5,''),$6::jsonb,$2,$2)`, org, user, action, entityType, entityID, string(raw))
	return err
}

// ---- Concesiones ----

func (s *store) grant(ctx context.Context, userID string) (g domain.Grant, err error) {
	err = s.tx.QueryRow(ctx, `SELECT g.user_id,u.email,u.display_name,g.granted_at,g.revoked_at FROM research_access_grants g JOIN application_users u ON u.id=g.user_id WHERE g.user_id=$1`, userID).
		Scan(&g.UserID, &g.Email, &g.DisplayName, &g.GrantedAt, &g.RevokedAt)
	return
}

func (s *store) UserByEmail(ctx context.Context, email string) (g domain.Grant, err error) {
	err = s.tx.QueryRow(ctx, `SELECT id,email,display_name FROM application_users WHERE lower(email)=$1 AND status='ACTIVE'`, email).Scan(&g.UserID, &g.Email, &g.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		err = domain.ErrUserNotFound
	}
	return
}

func (s *store) SaveGrant(ctx context.Context, by, userID string, at time.Time) (domain.Grant, error) {
	_, err := s.tx.Exec(ctx, `INSERT INTO research_access_grants(user_id,granted_by,granted_at,created_by,updated_by) VALUES($1,$2,$3,$2,$2)
		ON CONFLICT (user_id) DO UPDATE SET
		 granted_by=CASE WHEN research_access_grants.revoked_at IS NULL THEN research_access_grants.granted_by ELSE EXCLUDED.granted_by END,
		 granted_at=CASE WHEN research_access_grants.revoked_at IS NULL THEN research_access_grants.granted_at ELSE EXCLUDED.granted_at END,
		 revoked_at=NULL, revoked_by=NULL, updated_by=EXCLUDED.granted_by`, userID, by, at)
	if err != nil {
		return domain.Grant{}, err
	}
	return s.grant(ctx, userID)
}

func (s *store) RevokeGrant(ctx context.Context, by, userID string, at time.Time) error {
	tag, err := s.tx.Exec(ctx, `UPDATE research_access_grants SET revoked_at=$3,revoked_by=$2,updated_by=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, by, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		ok, err := exists(ctx, s.tx, `SELECT 1 FROM research_access_grants WHERE user_id=$1`, userID)
		if err != nil {
			return err
		}
		if !ok {
			return domain.ErrGrantNotFound
		}
	}
	return nil
}

func (s *store) Grants(ctx context.Context, w pagination.Window) ([]domain.Grant, error) {
	rows, err := s.tx.Query(ctx, `SELECT g.user_id,u.email,u.display_name,g.granted_at,g.revoked_at FROM research_access_grants g JOIN application_users u ON u.id=g.user_id
		WHERE ($1='' OR g.user_id>$1) ORDER BY g.user_id LIMIT $2`, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Grant{}
	for rows.Next() {
		var g domain.Grant
		if err = rows.Scan(&g.UserID, &g.Email, &g.DisplayName, &g.GrantedAt, &g.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---- Consentimiento y participante ----

const consentDocumentColumns = `version,title,body_text,responsible_investigator,status,published_at`

func scanConsentDocument(row pgx.Row) (d domain.ConsentDocument, err error) {
	err = row.Scan(&d.Version, &d.Title, &d.Text, &d.ResponsibleInvestigator, &d.Status, &d.PublishedAt)
	return
}

func (s *store) ActiveConsentDocument(ctx context.Context) (*domain.ConsentDocument, error) {
	d, err := scanConsentDocument(s.tx.QueryRow(ctx, `SELECT `+consentDocumentColumns+` FROM research_consent_documents WHERE status='ACTIVE'`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *store) PublishConsentDocument(ctx context.Context, user string, in domain.ConsentDocumentInput, at time.Time) (domain.ConsentDocument, error) {
	if _, err := s.tx.Exec(ctx, `UPDATE research_consent_documents SET status='ARCHIVED',updated_by=$1 WHERE status='ACTIVE'`, user); err != nil {
		return domain.ConsentDocument{}, err
	}
	d, err := scanConsentDocument(s.tx.QueryRow(ctx, `INSERT INTO research_consent_documents(version,title,body_text,responsible_investigator,status,published_at,created_by,updated_by)
		VALUES($1,$2,$3,$4,'ACTIVE',$5,$6,$6) RETURNING `+consentDocumentColumns, in.Version, in.Title, in.Text, in.ResponsibleInvestigator, at, user))
	if _, taken := uniqueViolation(err); taken {
		return d, domain.ErrConsentVersionTaken
	}
	return d, err
}

func (s *store) ConsentDocuments(ctx context.Context, w pagination.Window) ([]domain.ConsentDocument, error) {
	rows, err := s.tx.Query(ctx, `SELECT `+consentDocumentColumns+` FROM research_consent_documents WHERE ($1='' OR version>$1) ORDER BY version LIMIT $2`, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ConsentDocument{}
	for rows.Next() {
		d, err := scanConsentDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) FindParticipant(ctx context.Context, org, user string, lock bool) (*domain.Participant, error) {
	sql := `SELECT id::text,participant_code,status FROM research_participants WHERE organization_id=$1 AND user_id=$2`
	if lock {
		sql += ` FOR UPDATE`
	}
	var p domain.Participant
	err := s.tx.QueryRow(ctx, sql, org, user).Scan(&p.ID, &p.Code, &p.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *store) EnsureParticipant(ctx context.Context, org, user string, newCode func() (string, error)) (domain.Participant, error) {
	for attempt := 0; attempt < 5; attempt++ {
		code, err := newCode()
		if err != nil {
			return domain.Participant{}, err
		}
		var p domain.Participant
		// ON CONFLICT DO NOTHING evita abortar la transacción ante un código repetido
		// o una inscripción concurrente del mismo usuario.
		err = s.tx.QueryRow(ctx, `INSERT INTO research_participants(organization_id,user_id,participant_code,created_by,updated_by) VALUES($1,$2,$3,$2,$2)
			ON CONFLICT DO NOTHING RETURNING id::text,participant_code,status`, org, user, code).Scan(&p.ID, &p.Code, &p.Status)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return p, err
		}
		existing, err := s.FindParticipant(ctx, org, user, true)
		if err != nil {
			return p, err
		}
		if existing != nil {
			return *existing, nil
		}
	}
	return domain.Participant{}, domain.ErrParticipantCodeFailed
}

func (s *store) LatestConsent(ctx context.Context, org, participantID string) (*domain.Consent, error) {
	var c domain.Consent
	err := s.tx.QueryRow(ctx, `SELECT document_version,accepted,accepted_at,responsible_investigator FROM consent_records
		WHERE organization_id=$1 AND participant_id=$2 ORDER BY accepted_at DESC,created_at DESC,id DESC LIMIT 1`, org, participantID).
		Scan(&c.DocumentVersion, &c.Accepted, &c.At, &c.ResponsibleInvestigator)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *store) RecordConsent(ctx context.Context, org, user string, p domain.Participant, rec domain.Consent) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO consent_records(organization_id,participant_id,document_version,accepted,accepted_at,recorded_by,responsible_investigator,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$6,$6)`, org, p.ID, rec.DocumentVersion, rec.Accepted, rec.At, user, rec.ResponsibleInvestigator)
	if err != nil {
		return err
	}
	status := domain.ParticipantActive
	if !rec.Accepted {
		status = domain.ParticipantWithdrawn
	}
	_, err = s.tx.Exec(ctx, `UPDATE research_participants SET status=$3,updated_by=$4 WHERE organization_id=$1 AND id=$2`, org, p.ID, status, user)
	return err
}

var _ application.UnitOfWork = (*Repository)(nil)
var _ application.Store = (*store)(nil)
