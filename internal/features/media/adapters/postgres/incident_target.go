package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/jackc/pgx/v5"
)

// IncidentTarget bloquea el mismo agregado que la validación de cierre: las fotos
// confirmadas no pueden retirarse mientras otro proceso verifica la corrección.
func (s *store) IncidentTarget(ctx context.Context, user, org, incident, action, stage string, write bool) (t domain.Target, err error) {
	role, err := s.actor(ctx, user, org)
	if err != nil {
		return t, err
	}
	t = domain.Target{OrganizationID: org, UserID: user, Role: role, IncidentID: incident, ActionID: action, Stage: stage}
	clause := " FOR SHARE OF i,l"
	if write {
		clause = " FOR UPDATE OF i FOR SHARE OF l"
	}
	err = s.tx.QueryRow(ctx, `SELECT i.location_id::text,i.reported_by,i.status FROM incidents i JOIN locations l ON l.organization_id=i.organization_id AND l.id=i.location_id AND l.status='ACTIVE' WHERE i.organization_id=$1 AND i.id=$2`+clause, org, incident).Scan(&t.LocationID, &t.Reporter, &t.IncidentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if role != "ADMIN" && role != "SUPER_ADMIN" {
		var location string
		err = s.tx.QueryRow(ctx, `SELECT location_id::text FROM membership_locations WHERE organization_id=$1 AND user_id=$2 AND location_id=$3 FOR SHARE`, org, user, t.LocationID).Scan(&location)
		if errors.Is(err, pgx.ErrNoRows) {
			return t, domain.ErrDenied
		}
		if err != nil {
			return t, err
		}
	}
	if action != "" {
		err = s.tx.QueryRow(ctx, `SELECT responsible_user_id,status FROM corrective_actions WHERE organization_id=$1 AND incident_id=$2 AND id=$3 FOR SHARE`, org, incident, action).Scan(&t.Responsible, &t.ActionStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return t, domain.ErrNotFound
		}
		return t, err
	}
	var assigned bool
	err = s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM corrective_actions WHERE organization_id=$1 AND incident_id=$2 AND responsible_user_id=$3)`, org, incident, user).Scan(&assigned)
	if assigned {
		t.Responsible = user
	}
	return t, err
}

const incidentFileFields = `m.id::text,m.organization_id,COALESCE(m.location_id::text,''),''::text,''::text,m.original_name,m.mime_type,m.size_bytes,COALESCE(m.sha256,''),m.object_key,m.status,COALESCE(m.created_by,''),m.created_at,m.uploaded_at,im.incident_id::text,COALESCE(im.corrective_action_id::text,''),im.stage`

func (s *store) incidentList(ctx context.Context, t domain.Target) ([]domain.File, error) {
	rows, e := s.tx.Query(ctx, `SELECT `+incidentFileFields+` FROM incident_media im JOIN media_files m ON m.organization_id=im.organization_id AND m.id=im.media_file_id WHERE im.organization_id=$1 AND im.incident_id=$2 AND im.stage=$3 AND COALESCE(im.corrective_action_id::text,'')=$4 AND m.status<>'DELETED' AND m.location_id=$5`+unambiguousFile+` ORDER BY m.created_at,m.id`, t.OrganizationID, t.IncidentID, t.Stage, t.ActionID, t.LocationID)
	if e != nil {
		return nil, e
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
