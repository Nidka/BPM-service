package postgres

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/jackc/pgx/v5"
)

const incidentFields = `i.id::text,i.location_id::text,i.catalog_id::text,c.name,c.area,c.risk_level,i.status,COALESCE(i.description,''),i.reported_by,u.display_name,i.created_at,i.row_version,c.version_number,c.normative_criterion,c.evidence_required,i.closed_at`
const incidentJoins = ` FROM incidents i JOIN incident_catalog c ON c.organization_id=i.organization_id AND c.id=i.catalog_id JOIN application_users u ON u.id=i.reported_by`

func scanIncident(row pgx.Row) (v domain.Incident, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.CatalogID, &v.Name, &v.Area, &v.RiskLevel, &v.Status, &v.Description, &v.ReportedBy, &v.ReportedByName, &v.CreatedAt, &v.RowVersion, &v.CatalogVersion, &v.NormativeCriterion, &v.EvidenceRequired, &v.ClosedAt)
	return v, missing(e)
}
func (s *store) Incident(ctx context.Context, org, id string, lock bool) (domain.Incident, error) {
	clause := ""
	if lock {
		clause = " FOR UPDATE OF i"
	}
	v, e := scanIncident(s.tx.QueryRow(ctx, `SELECT `+incidentFields+incidentJoins+` WHERE i.organization_id=$1 AND i.id=$2`+clause, org, id))
	if e != nil {
		return v, e
	}
	rows, e := s.tx.Query(ctx, `SELECT a.id::text,a.responsible_user_id,u.display_name,a.description,a.due_at,a.status,COALESCE(a.resolution_note,''),a.resolved_at,a.verified_at,a.verified_by FROM corrective_actions a JOIN application_users u ON u.id=a.responsible_user_id WHERE a.organization_id=$1 AND a.incident_id=$2 ORDER BY a.created_at,a.id`, org, id)
	if e != nil {
		return v, e
	}
	v.Actions = []domain.Action{}
	for rows.Next() {
		var x domain.Action
		if e = rows.Scan(&x.ID, &x.ResponsibleUserID, &x.ResponsibleName, &x.Description, &x.DueAt, &x.Status, &x.ResolutionNote, &x.ResolvedAt, &x.VerifiedAt, &x.VerifiedBy); e != nil {
			rows.Close()
			return v, e
		}
		v.Actions = append(v.Actions, x)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return v, e
	}
	action := ""
	if len(v.Actions) == 1 {
		action = v.Actions[0].ID
	}
	e = s.tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE im.stage='BEFORE' AND im.corrective_action_id IS NULL),count(*) FILTER(WHERE im.stage='AFTER' AND im.corrective_action_id::text=$3) FROM incident_media im JOIN media_files m ON m.organization_id=im.organization_id AND m.id=im.media_file_id AND m.location_id=$4 AND m.status='AVAILABLE' AND m.mime_type IN ('image/jpeg','image/png') WHERE im.organization_id=$1 AND im.incident_id=$2 AND (
 (SELECT count(*) FROM incident_media target WHERE target.organization_id=m.organization_id AND target.media_file_id=m.id) +
 (SELECT count(*) FROM checklist_media target WHERE target.organization_id=m.organization_id AND target.media_file_id=m.id) +
 (SELECT count(*) FROM cleaning_media target WHERE target.organization_id=m.organization_id AND target.media_file_id=m.id)
 )=1`, org, id, action, v.LocationID).Scan(&v.BeforeCount, &v.AfterCount)
	return v, e
}
func (s *store) InsertIncident(ctx context.Context, a domain.Actor, org string, v domain.Report, hash string) (domain.Incident, error) {
	_, e := s.tx.Exec(ctx, `INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,description,client_operation_id,client_created_at,request_hash,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$5,$5)`, v.ID, org, v.LocationID, v.CatalogID, a.ID, v.Description, v.ClientOperationID, v.ClientCreatedAt, hash)
	if e != nil {
		return domain.Incident{}, conflict(e)
	}
	return s.Incident(ctx, org, v.ID, false)
}
func (s *store) SaveIncident(ctx context.Context, a domain.Actor, org string, v domain.Incident, version int) error {
	tag, e := s.tx.Exec(ctx, `UPDATE incidents SET status=$3,row_version=$4,closed_at=$5,updated_by=$6 WHERE organization_id=$1 AND id=$2 AND row_version=$7`, org, v.ID, v.Status, v.RowVersion, v.ClosedAt, a.ID, version)
	if e != nil {
		return conflict(e)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if len(v.Actions) != 1 {
		return domain.ErrConflict
	}
	x := v.Actions[0]
	tag, e = s.tx.Exec(ctx, `INSERT INTO corrective_actions(id,organization_id,incident_id,responsible_user_id,description,due_at,status,resolution_note,resolved_at,verified_at,verified_by,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,$12,$12) ON CONFLICT (organization_id,id) DO UPDATE SET status=EXCLUDED.status,resolution_note=EXCLUDED.resolution_note,resolved_at=EXCLUDED.resolved_at,verified_at=EXCLUDED.verified_at,verified_by=EXCLUDED.verified_by,updated_by=EXCLUDED.updated_by WHERE corrective_actions.incident_id=EXCLUDED.incident_id`, x.ID, org, v.ID, x.ResponsibleUserID, x.Description, x.DueAt, x.Status, x.ResolutionNote, x.ResolvedAt, x.VerifiedAt, x.VerifiedBy, a.ID)
	if e != nil {
		return conflict(e)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
