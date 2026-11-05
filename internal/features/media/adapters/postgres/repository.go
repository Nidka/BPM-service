package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/media/application"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }
type store struct{ tx pgx.Tx }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool} }

// RequireSchema evita servir controles con un esquema de evidencias incompatible.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT checklist_item_id FROM checklist_media LIMIT 0`)
	if err != nil {
		return errors.New("private evidence migration V4 is required")
	}
	rows.Close()
	return rows.Err()
}

func (r *Repository) Within(ctx context.Context, fn func(application.Store) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = fn(&store{tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *store) actor(ctx context.Context, user, org string) (string, error) {
	var platform, role string
	err := s.tx.QueryRow(ctx, `SELECT u.platform_role FROM application_users u JOIN organizations o ON o.id=$2 AND o.status='ACTIVE' WHERE u.id=$1 AND u.status='ACTIVE' FOR SHARE OF u,o`, user, org).Scan(&platform)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrDenied
	}
	if err != nil {
		return "", err
	}
	if platform == "SUPER_ADMIN" {
		return "SUPER_ADMIN", nil
	}
	err = s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' FOR SHARE`, org, user).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrDenied
	}
	return role, err
}
func (s *store) Target(ctx context.Context, user, org, run, item string, lock bool) (target domain.Target, err error) {
	role, err := s.actor(ctx, user, org)
	if err != nil {
		return target, err
	}
	clause := " FOR SHARE OF r,l"
	if lock {
		clause = " FOR UPDATE OF r FOR SHARE OF l"
	}
	target = domain.Target{OrganizationID: org, RunID: run, ItemID: item, UserID: user, Role: role}
	err = s.tx.QueryRow(ctx, `SELECT r.location_id::text,r.performed_by,r.status FROM checklist_runs r
 JOIN locations l ON l.organization_id=r.organization_id AND l.id=r.location_id AND l.status='ACTIVE'
 JOIN checklist_items i ON i.organization_id=r.organization_id AND i.template_version_id=r.template_version_id AND i.id=$3
 WHERE r.organization_id=$1 AND r.id=$2`+clause, org, run, item).Scan(&target.LocationID, &target.RunOwner, &target.RunStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, domain.ErrNotFound
	}
	if err != nil {
		return target, err
	}
	if role != "ADMIN" && role != "SUPER_ADMIN" {
		var site string
		err = s.tx.QueryRow(ctx, `SELECT location_id::text FROM membership_locations WHERE organization_id=$1 AND user_id=$2 AND location_id=$3 FOR SHARE`, org, user, target.LocationID).Scan(&site)
		if errors.Is(err, pgx.ErrNoRows) {
			return target, domain.ErrDenied
		}
	}
	return target, err
}

const fileFields = `m.id::text,m.organization_id,COALESCE(m.location_id::text,''),cm.checklist_run_id::text,COALESCE(cm.checklist_item_id::text,''),m.original_name,m.mime_type,m.size_bytes,COALESCE(m.sha256,''),m.object_key,m.status,COALESCE(m.created_by,''),m.created_at,m.uploaded_at,''::text,''::text,''::text`

func scanFile(row pgx.Row) (f domain.File, err error) {
	err = row.Scan(&f.ID, &f.OrganizationID, &f.LocationID, &f.RunID, &f.ItemID, &f.OriginalName, &f.MIMEType, &f.SizeBytes, &f.SHA256, &f.ObjectKey, &f.Status, &f.CreatedBy, &f.CreatedAt, &f.UploadedAt, &f.IncidentID, &f.ActionID, &f.Stage)
	if errors.Is(err, pgx.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return
}
func (s *store) File(ctx context.Context, org, id string, lock bool) (domain.File, error) {
	clause := ""
	if lock {
		clause = " FOR UPDATE OF m"
	}
	file, err := scanFile(s.tx.QueryRow(ctx, `SELECT `+fileFields+` FROM media_files m JOIN checklist_media cm ON cm.organization_id=m.organization_id AND cm.media_file_id=m.id WHERE m.organization_id=$1 AND m.id=$2`+unambiguousFile+clause, org, id))
	if errors.Is(err, domain.ErrNotFound) {
		return scanFile(s.tx.QueryRow(ctx, `SELECT `+incidentFileFields+` FROM media_files m JOIN incident_media im ON im.organization_id=m.organization_id AND im.media_file_id=m.id WHERE m.organization_id=$1 AND m.id=$2`+unambiguousFile+clause, org, id))
	}
	return file, err
}
func (s *store) List(ctx context.Context, t domain.Target) ([]domain.File, error) {
	if t.IncidentID != "" {
		return s.incidentList(ctx, t)
	}
	rows, err := s.tx.Query(ctx, `SELECT `+fileFields+` FROM checklist_media cm JOIN media_files m ON m.organization_id=cm.organization_id AND m.id=cm.media_file_id WHERE cm.organization_id=$1 AND cm.checklist_run_id=$2 AND cm.checklist_item_id=$3 AND m.status<>'DELETED' AND m.location_id=$4`+unambiguousFile+` ORDER BY m.created_at,m.id LIMIT 3`, t.OrganizationID, t.RunID, t.ItemID, t.LocationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.File{}
	for rows.Next() {
		f, e := scanFile(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *store) Reserve(ctx context.Context, t domain.Target, f domain.File, max int) (domain.File, error) {
	existing, err := s.File(ctx, t.OrganizationID, f.ID, true)
	if err == nil {
		if existing.CreatedBy != t.UserID {
			return domain.File{}, domain.ErrConflict
		}
		err = existing.Match(domain.UploadInput{ID: f.ID, RunID: f.RunID, ItemID: f.ItemID, IncidentID: f.IncidentID, ActionID: f.ActionID, Stage: f.Stage, OriginalName: f.OriginalName, MIMEType: f.MIMEType, SizeBytes: f.SizeBytes, SHA256: f.SHA256})
		return existing, err
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.File{}, err
	}
	var count int
	if t.IncidentID != "" {
		err = s.tx.QueryRow(ctx, `SELECT count(*) FROM incident_media im JOIN media_files m ON m.organization_id=im.organization_id AND m.id=im.media_file_id WHERE im.organization_id=$1 AND im.incident_id=$2 AND im.stage=$3 AND COALESCE(im.corrective_action_id::text,'')=$4 AND m.status<>'DELETED'`, t.OrganizationID, t.IncidentID, t.Stage, t.ActionID).Scan(&count)
	} else {
		err = s.tx.QueryRow(ctx, `SELECT count(*) FROM checklist_media cm JOIN media_files m ON m.organization_id=cm.organization_id AND m.id=cm.media_file_id WHERE cm.organization_id=$1 AND cm.checklist_run_id=$2 AND cm.checklist_item_id=$3 AND m.status<>'DELETED'`, t.OrganizationID, t.RunID, t.ItemID).Scan(&count)
	}
	if err != nil {
		return domain.File{}, err
	}
	if count >= max {
		return domain.File{}, domain.ErrLimit
	}
	tag, err := s.tx.Exec(ctx, `INSERT INTO media_files(id,organization_id,location_id,object_key,storage_url,original_name,mime_type,size_bytes,sha256,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) ON CONFLICT DO NOTHING`, f.ID, f.OrganizationID, f.LocationID, f.ObjectKey, "r2://"+f.ObjectKey, f.OriginalName, f.MIMEType, f.SizeBytes, f.SHA256, t.UserID)
	if err != nil {
		return domain.File{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.File{}, domain.ErrConflict
	}
	if t.IncidentID != "" {
		_, err = s.tx.Exec(ctx, `INSERT INTO incident_media(organization_id,incident_id,corrective_action_id,media_file_id,stage,created_by,updated_by) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$6)`, t.OrganizationID, t.IncidentID, t.ActionID, f.ID, t.Stage, t.UserID)
	} else {
		_, err = s.tx.Exec(ctx, `INSERT INTO checklist_media(organization_id,checklist_run_id,checklist_item_id,media_file_id,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5)`, t.OrganizationID, t.RunID, t.ItemID, f.ID, t.UserID)
	}
	if err != nil {
		return domain.File{}, err
	}
	if err = s.audit(ctx, t.UserID, f, "media.upload.reserved"); err != nil {
		return domain.File{}, err
	}
	return s.File(ctx, t.OrganizationID, f.ID, false)
}
func (s *store) SetStatus(ctx context.Context, user string, f domain.File, status string) (domain.File, error) {
	_, err := s.tx.Exec(ctx, `UPDATE media_files SET status=$3::varchar(24),uploaded_at=CASE WHEN $3::varchar(24)='AVAILABLE' THEN NOW() ELSE uploaded_at END,updated_by=$4 WHERE organization_id=$1 AND id=$2`, f.OrganizationID, f.ID, status, user)
	if err != nil {
		return domain.File{}, err
	}
	action := map[string]string{"AVAILABLE": "media.upload.confirmed", "FAILED": "media.upload.failed", "DELETED": "media.evidence.removed"}[status]
	if action == "" {
		return domain.File{}, domain.ErrConflict
	}
	if err = s.audit(ctx, user, f, action); err != nil {
		return domain.File{}, err
	}
	return s.File(ctx, f.OrganizationID, f.ID, false)
}
func (s *store) audit(ctx context.Context, user string, f domain.File, action string) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,created_by,updated_by) VALUES($1,$2,$3,'media_file',$4,$2,$2)`, f.OrganizationID, user, action, f.ID)
	return err
}

var _ application.UnitOfWork = (*Repository)(nil)

// Un archivo heredado con varios destinos no autoriza lecturas ni mutaciones por
// una asociación alternativa. El operador debe resolver su procedencia sin borrarlo.
const unambiguousFile = ` AND ((SELECT count(*) FROM checklist_media x WHERE x.organization_id=m.organization_id AND x.media_file_id=m.id)+(SELECT count(*) FROM incident_media x WHERE x.organization_id=m.organization_id AND x.media_file_id=m.id)+(SELECT count(*) FROM cleaning_media x WHERE x.organization_id=m.organization_id AND x.media_file_id=m.id))=1`
