package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/checklists/application"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) WithinAssignments(ctx context.Context, fn func(application.AssignmentStore) error) error {
	return r.within(ctx, func(s *store) error { return fn(s) })
}

const assignmentSelect = `SELECT a.id::text,a.template_id::text,t.name,v.id::text,a.location_id::text,l.name,
 a.frequency,COALESCE(to_char(a.due_time,'HH24:MI'),''),l.timezone,a.active,a.created_at
 FROM checklist_assignments a
 JOIN checklist_templates t ON t.organization_id=a.organization_id AND t.id=a.template_id AND t.status='ACTIVE'
 JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id AND l.status='ACTIVE'
 JOIN LATERAL (SELECT id FROM checklist_template_versions WHERE organization_id=t.organization_id AND template_id=t.id AND status='PUBLISHED' ORDER BY version_number DESC LIMIT 1) v ON true
 WHERE a.organization_id=$1
 AND ($3 IN ('ADMIN','SUPER_ADMIN') OR EXISTS(SELECT 1 FROM organization_memberships m
 JOIN membership_locations ml ON ml.organization_id=m.organization_id AND ml.user_id=m.user_id AND ml.location_id=a.location_id
 WHERE m.organization_id=a.organization_id AND m.user_id=$2 AND m.status='ACTIVE' AND m.role='CHEF'))`

func scanAssignment(row pgx.Row) (a domain.Assignment, err error) {
	err = row.Scan(&a.ID, &a.TemplateID, &a.TemplateName, &a.TemplateVersionID, &a.LocationID, &a.LocationName,
		&a.Frequency, &a.DueTime, &a.Timezone, &a.Active, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return
}

func (s *store) AssignmentPage(ctx context.Context, actor domain.Actor, org string, w pagination.Window) ([]domain.Assignment, error) {
	rows, err := s.tx.Query(ctx, assignmentSelect+` AND a.active
 AND ($4='' OR strpos(lower(t.name||' '||l.name),lower($4))>0)
 AND ($6='' OR (t.name,a.id::text)>($5,$6)) ORDER BY t.name,a.id::text LIMIT $7`, org, actor.ID, actor.Role, w.Query, w.After.Key, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Assignment{}
	for rows.Next() {
		a, e := scanAssignment(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) Assignment(ctx context.Context, actor domain.Actor, org, id string, lock bool) (domain.Assignment, error) {
	query := assignmentSelect + ` AND a.id=$4`
	if lock {
		query += ` FOR UPDATE OF a`
	}
	return scanAssignment(s.tx.QueryRow(ctx, query, org, actor.ID, actor.Role, id))
}

func (s *store) lockTemplate(ctx context.Context, org, id string) error {
	var found string
	err := s.tx.QueryRow(ctx, `SELECT t.id::text FROM checklist_templates t WHERE t.organization_id=$1 AND t.id=$2 AND t.status='ACTIVE'
 AND EXISTS(SELECT 1 FROM checklist_template_versions v WHERE v.organization_id=t.organization_id AND v.template_id=t.id AND v.status='PUBLISHED') FOR UPDATE OF t`, org, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func (s *store) CreateAssignment(ctx context.Context, actor domain.Actor, org string, a domain.Assignment) (domain.Assignment, error) {
	// Serializa programación e inicio de revisiones para la misma plantilla, también entre réplicas.
	if err := s.lockTemplate(ctx, org, a.TemplateID); err != nil {
		return domain.Assignment{}, err
	}
	var location string
	err := s.tx.QueryRow(ctx, `SELECT id::text FROM locations WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR SHARE`, org, a.LocationID).Scan(&location)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Assignment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Assignment{}, err
	}
	previous, err := s.Assignment(ctx, actor, org, a.ID, false)
	if err == nil {
		if previous.Active && previous.TemplateID == a.TemplateID && previous.LocationID == a.LocationID && previous.Frequency == a.Frequency && previous.DueTime == a.DueTime {
			return previous, nil
		}
		return domain.Assignment{}, domain.ErrConflict
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Assignment{}, err
	}
	var exists bool
	err = s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM checklist_assignments WHERE organization_id=$1 AND location_id=$2 AND template_id=$3 AND active)`, org, a.LocationID, a.TemplateID).Scan(&exists)
	if err != nil {
		return domain.Assignment{}, err
	}
	if exists {
		return domain.Assignment{}, domain.ErrConflict
	}
	_, err = s.tx.Exec(ctx, `INSERT INTO checklist_assignments(id,organization_id,location_id,template_id,frequency,due_time,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::time,$7,$7)`, a.ID, org, a.LocationID, a.TemplateID, a.Frequency, a.DueTime, actor.ID)
	if err != nil {
		return domain.Assignment{}, pgConflict(err)
	}
	if err = s.audit(ctx, actor, org, "checklist.assignment.created", "checklist_assignment", a.ID); err != nil {
		return domain.Assignment{}, err
	}
	return s.Assignment(ctx, actor, org, a.ID, false)
}

func (s *store) DeactivateAssignment(ctx context.Context, actor domain.Actor, org, id string) (domain.Assignment, error) {
	a, err := s.Assignment(ctx, actor, org, id, false)
	if err != nil {
		return a, err
	}
	if err = s.lockTemplate(ctx, org, a.TemplateID); err != nil {
		return a, err
	}
	a, err = s.Assignment(ctx, actor, org, id, true)
	if err != nil {
		return a, err
	}
	if a.Active {
		if _, err = s.tx.Exec(ctx, `UPDATE checklist_assignments SET active=false,updated_by=$3 WHERE organization_id=$1 AND id=$2`, org, id, actor.ID); err != nil {
			return a, err
		}
		if err = s.audit(ctx, actor, org, "checklist.assignment.deactivated", "checklist_assignment", id); err != nil {
			return a, err
		}
	}
	return s.Assignment(ctx, actor, org, id, false)
}

func (s *store) PeriodRuns(ctx context.Context, org string, periods []domain.AssignmentPeriod) (map[string]domain.AssignedRun, error) {
	out := map[string]domain.AssignedRun{}
	if len(periods) == 0 {
		return out, nil
	}
	ids := make([]string, len(periods))
	starts := make([]time.Time, len(periods))
	ends := make([]*time.Time, len(periods))
	for i, p := range periods {
		ids[i] = p.AssignmentID
		starts[i] = p.StartsAt
		ends[i] = p.EndsAt
	}
	rows, err := s.tx.Query(ctx, `SELECT p.id::text,r.id::text,r.status,r.performed_by,u.display_name,r.created_at,r.submitted_at
 FROM unnest($2::uuid[],$3::timestamptz[],$4::timestamptz[]) AS p(id,starts_at,ends_at)
 JOIN LATERAL (SELECT id,status,performed_by,created_at,submitted_at FROM checklist_runs
 WHERE organization_id=$1 AND assignment_id=p.id AND status IN ('DRAFT','SUBMITTED') AND created_at>=p.starts_at AND (p.ends_at IS NULL OR created_at<p.ends_at)
 ORDER BY created_at DESC,id DESC LIMIT 1) r ON true JOIN application_users u ON u.id=r.performed_by`, org, ids, starts, ends)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var r domain.AssignedRun
		if err = rows.Scan(&id, &r.ID, &r.Status, &r.PerformedBy, &r.PerformedByName, &r.CreatedAt, &r.SubmittedAt); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}

func (s *store) StartedRun(ctx context.Context, org, id string) (bool, error) {
	var found bool
	err := s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM checklist_runs WHERE organization_id=$1 AND id=$2)`, org, id).Scan(&found)
	return found, err
}

func (s *store) RunSchedule(ctx context.Context, actor domain.Actor, org string, v domain.Run) (domain.Assignment, error) {
	var templateID string
	err := s.tx.QueryRow(ctx, `SELECT t.id::text FROM checklist_template_versions v JOIN checklist_templates t ON t.organization_id=v.organization_id AND t.id=v.template_id
 WHERE v.organization_id=$1 AND v.id=$2 AND v.status='PUBLISHED' AND t.status='ACTIVE' FOR SHARE OF t`, org, v.TemplateVersionID).Scan(&templateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Assignment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Assignment{}, err
	}
	rows, err := s.tx.Query(ctx, `SELECT id::text FROM checklist_assignments WHERE organization_id=$1 AND location_id=$2 AND template_id=$3 AND active ORDER BY id LIMIT 2 FOR UPDATE`, org, v.LocationID, templateID)
	if err != nil {
		return domain.Assignment{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return domain.Assignment{}, err
	}
	if len(ids) > 1 {
		return domain.Assignment{}, domain.ErrConflict
	}
	if len(ids) == 0 {
		if v.AssignmentID != "" {
			return domain.Assignment{}, domain.ErrNotFound
		}
		return domain.Assignment{}, nil
	}
	if v.AssignmentID == "" {
		return domain.Assignment{}, domain.ErrAssignmentRequired
	}
	if v.AssignmentID != ids[0] {
		return domain.Assignment{}, domain.ErrNotFound
	}
	a, err := s.Assignment(ctx, actor, org, ids[0], false)
	if err != nil {
		return a, err
	}
	if a.TemplateVersionID != v.TemplateVersionID {
		return a, domain.ErrConflict
	}
	return a, nil
}
