package postgres

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"time"
)

func pageTime(w pagination.Window) (time.Time, error) {
	if w.After.ID == "" {
		return time.Time{}, nil
	}
	if !domain.ValidID(w.After.ID) {
		return time.Time{}, pagination.ErrInvalid
	}
	at, e := time.Parse(time.RFC3339Nano, w.After.Key)
	if e != nil {
		return at, pagination.ErrInvalid
	}
	return at, nil
}
func (s *store) Incidents(ctx context.Context, a domain.Actor, org, site, status string, w pagination.Window) ([]domain.Incident, error) {
	at, e := pageTime(w)
	if e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+incidentFields+incidentJoins+` WHERE i.organization_id=$1 AND i.location_id=$2 AND ($3='' OR i.status=$3) AND ($4<>'WORKER' OR i.reported_by=$5 OR EXISTS(SELECT 1 FROM corrective_actions ca WHERE ca.organization_id=i.organization_id AND ca.incident_id=i.id AND ca.responsible_user_id=$5)) AND ($6='' OR strpos(lower(c.name||' '||c.area||' '||COALESCE(i.description,'')),lower($6))>0) AND ($7='' OR i.created_at<$8::timestamptz OR (i.created_at=$8::timestamptz AND i.id::text>$7)) ORDER BY i.created_at DESC,i.id LIMIT $9`, org, site, status, a.Role, a.ID, w.Query, w.After.ID, at, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Incident{}
	for rows.Next() {
		v, e := scanIncident(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) Assignees(ctx context.Context, org, site string, w pagination.Window) ([]domain.Assignee, error) {
	rows, e := s.tx.Query(ctx, `SELECT u.id,u.display_name,m.role FROM application_users u JOIN organization_memberships m ON m.user_id=u.id AND m.organization_id=$1 JOIN membership_locations ml ON ml.organization_id=m.organization_id AND ml.user_id=m.user_id AND ml.location_id=$2 WHERE u.status='ACTIVE' AND m.status='ACTIVE' AND m.role IN ('CHEF','WORKER') AND ($3='' OR strpos(lower(u.display_name||' '||u.email),lower($3))>0) AND ($4='' OR (lower(u.display_name),u.id)>($4,$5)) ORDER BY lower(u.display_name),u.id LIMIT $6`, org, site, w.Query, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Assignee{}
	for rows.Next() {
		var v domain.Assignee
		if e = rows.Scan(&v.ID, &v.DisplayName, &v.Role); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) History(ctx context.Context, org, id string, w pagination.Window) ([]domain.Event, error) {
	at, e := pageTime(w)
	if e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT e.id::text,e.operation_id::text,COALESCE(e.from_status,''),e.to_status,u.display_name,e.note,e.created_at FROM incident_events e JOIN application_users u ON u.id=e.actor_user_id WHERE e.organization_id=$1 AND e.incident_id=$2 AND ($3='' OR e.created_at<$4::timestamptz OR (e.created_at=$4::timestamptz AND e.id::text>$3)) ORDER BY e.created_at DESC,e.id LIMIT $5`, org, id, w.After.ID, at, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var v domain.Event
		if e = rows.Scan(&v.ID, &v.OperationID, &v.FromStatus, &v.ToStatus, &v.ActorName, &v.Note, &v.CreatedAt); e != nil {
			return nil, e
		}
		v.CreatedAt = v.CreatedAt.UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
