package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

// externalProvider y externalID adaptan el video externo de una lección a columnas nulables.
func externalProvider(l domain.Lesson) *string {
	if l.ExternalVideo == nil {
		return nil
	}
	return &l.ExternalVideo.Provider
}

func externalID(l domain.Lesson) *string {
	if l.ExternalVideo == nil {
		return nil
	}
	return &l.ExternalVideo.VideoID
}

// afterKey convierte el cursor (fecha RFC3339 e id) en parámetros tipados; nil si no hay cursor.
func afterKey(w pagination.Window) (*time.Time, *string, error) {
	if w.After.ID == "" {
		return nil, nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, w.After.Key)
	if err != nil {
		return nil, nil, pagination.ErrInvalid
	}
	id := w.After.ID
	return &t, &id, nil
}

func (s *store) PublishedCourse(ctx context.Context, org, courseID string) (domain.Course, error) {
	var c domain.Course
	err := s.tx.QueryRow(ctx, `SELECT id::text,code,title,COALESCE(description,''),estimated_duration_minutes,status FROM training_courses WHERE organization_id=$1 AND id=$2`, org, courseID).
		Scan(&c.ID, &c.Code, &c.Title, &c.Description, &c.EstimatedDurationMinutes, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrCourseNotFound
	}
	if err != nil {
		return c, err
	}
	if c.Status != domain.StatusPublished {
		return c, domain.ErrCourseNotPublished
	}
	return c, nil
}

func (s *store) ResolveTarget(ctx context.Context, org string, t domain.AssignmentTarget) error {
	if t.UserID != "" {
		var one int
		err := s.tx.QueryRow(ctx, `SELECT 1 FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE' WHERE m.organization_id=$1 AND m.user_id=$2 AND m.status='ACTIVE'`, org, t.UserID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrAssigneeNotFound
		}
		if err != nil {
			return err
		}
	}
	if t.LocationID != "" {
		var one int
		err := s.tx.QueryRow(ctx, `SELECT 1 FROM locations WHERE organization_id=$1 AND id=$2 AND status='ACTIVE'`, org, t.LocationID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrLocationNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}

const assignmentSelect = `SELECT x.id::text,x.course_id::text,c.code,c.title,COALESCE(x.target_user_id,''),COALESCE(u.display_name,''),COALESCE(x.target_role,''),COALESCE(x.location_id::text,''),COALESCE(l.name,''),x.due_at,x.status,x.created_at,x.cancelled_at
 FROM training_assignments x JOIN training_courses c ON c.organization_id=x.organization_id AND c.id=x.course_id
 LEFT JOIN application_users u ON u.id=x.target_user_id
 LEFT JOIN locations l ON l.organization_id=x.organization_id AND l.id=x.location_id `

func scanAssignment(row pgx.Row) (domain.Assignment, error) {
	var a domain.Assignment
	err := row.Scan(&a.ID, &a.CourseID, &a.CourseCode, &a.CourseTitle, &a.Target.UserID, &a.UserName, &a.Target.Role, &a.Target.LocationID, &a.LocationName, &a.DueAt, &a.Status, &a.CreatedAt, &a.CancelledAt)
	switch {
	case a.Target.UserID != "":
		a.Target.Type = domain.TargetUser
	case a.Target.Role != "":
		a.Target.Type = domain.TargetRole
	default:
		a.Target.Type = domain.TargetLocation
	}
	a.CreatedAt = a.CreatedAt.UTC()
	if a.DueAt != nil {
		due := a.DueAt.UTC()
		a.DueAt = &due
	}
	return a, err
}

func (s *store) FindAssignment(ctx context.Context, org, id string) (domain.Assignment, bool, error) {
	a, err := scanAssignment(s.tx.QueryRow(ctx, assignmentSelect+`WHERE x.organization_id=$1 AND x.id=$2`, org, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (s *store) InsertAssignment(ctx context.Context, org, user string, a domain.NewAssignment) (domain.Assignment, error) {
	_, err := s.tx.Exec(ctx, `INSERT INTO training_assignments(id,organization_id,location_id,course_id,target_user_id,target_role,due_at,status,created_by,updated_by)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,NULLIF($5,''),NULLIF($6,''),$7,'ACTIVE',$8,$8)`,
		a.ID, org, a.Target.LocationID, a.CourseID, a.Target.UserID, a.Target.Role, a.DueAt, user)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Assignment{}, domain.ErrAssignmentExists
		}
		return domain.Assignment{}, err
	}
	out, _, err := s.FindAssignment(ctx, org, a.ID)
	return out, err
}

// EnsureAssignmentAlertRule crea, una sola vez por empresa, la regla TRAINING_ASSIGNED
// de alcance global que consume el evaluador de alertas (features/alerts). Si ya
// existe cualquier regla global de ese tipo (activa o no) se respeta la decisión del
// administrador. Un bloqueo asesor evita reglas duplicadas por carreras.
func (s *store) EnsureAssignmentAlertRule(ctx context.Context, org, user string) error {
	if _, err := s.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('training-alert-rule:' || $1, 0))`, org); err != nil {
		return err
	}
	_, err := s.tx.Exec(ctx, `INSERT INTO alert_rules(organization_id,location_id,name,alert_type,configuration,active,email_enabled,push_enabled,created_by,updated_by)
 SELECT $1::varchar,NULL,'Capacitación asignada','TRAINING_ASSIGNED','{}'::jsonb,TRUE,FALSE,TRUE,$2::varchar,$2::varchar
 WHERE NOT EXISTS (SELECT 1 FROM alert_rules WHERE organization_id=$1 AND alert_type='TRAINING_ASSIGNED' AND location_id IS NULL)`, org, user)
	return err
}

func (s *store) ListAssignments(ctx context.Context, org string, f domain.AssignmentFilter, w pagination.Window) ([]domain.Assignment, error) {
	ts, id, err := afterKey(w)
	if err != nil {
		return nil, err
	}
	rows, err := s.tx.Query(ctx, assignmentSelect+`WHERE x.organization_id=$1 AND ($2='' OR x.status=$2) AND ($3='' OR x.course_id::text=$3)
 AND ($4::timestamptz IS NULL OR (x.created_at,x.id)<($4,$5::uuid)) ORDER BY x.created_at DESC,x.id DESC LIMIT $6`, org, f.Status, f.CourseID, ts, id, w.Size+1)
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

func (s *store) CancelAssignment(ctx context.Context, org, user, id string, now time.Time) (domain.Assignment, error) {
	if _, err := s.tx.Exec(ctx, `UPDATE training_assignments SET status='CANCELLED',cancelled_at=$3,cancelled_by=$4,updated_by=$4 WHERE organization_id=$1 AND id=$2 AND status='ACTIVE'`, org, id, now, user); err != nil {
		return domain.Assignment{}, err
	}
	a, found, err := s.FindAssignment(ctx, org, id)
	if err != nil {
		return a, err
	}
	if !found {
		return a, domain.ErrAssignmentNotFound
	}
	return a, nil
}

// applicableAssignments selecciona, por curso, la asignación activa aplicable al
// usuario con la fecha límite más próxima. Aplica por persona, por rol o por sede
// (el usuario debe pertenecer a la sede); una asignación con rol y sede exige ambas.
const applicableAssignments = `SELECT DISTINCT ON (x.course_id) x.id,x.course_id,x.due_at,x.created_at
 FROM training_assignments x JOIN organization_memberships me ON me.organization_id=x.organization_id AND me.user_id=$2 AND me.status='ACTIVE'
 WHERE x.organization_id=$1 AND x.status='ACTIVE'
 AND (x.target_user_id IS NULL OR x.target_user_id=$2)
 AND (x.target_role IS NULL OR x.target_role=me.role)
 AND (x.location_id IS NULL OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=x.organization_id AND ml.user_id=$2 AND ml.location_id=x.location_id))
 ORDER BY x.course_id,x.due_at NULLS LAST,x.created_at,x.id`

func (s *store) MyAssignments(ctx context.Context, org, user string, w pagination.Window) ([]domain.MyAssignment, error) {
	ts, id, err := afterKey(w)
	if err != nil {
		return nil, err
	}
	rows, err := s.tx.Query(ctx, `WITH applicable AS (`+applicableAssignments+`)
 SELECT a.id::text,c.id::text,c.code,c.title,COALESCE(c.description,''),c.estimated_duration_minutes,a.due_at,a.created_at,COALESCE(p.status,'NOT_STARTED'),p.completed_at,p.best_score
 FROM applicable a JOIN training_courses c ON c.organization_id=$1 AND c.id=a.course_id AND c.status='PUBLISHED'
 LEFT JOIN training_progress p ON p.organization_id=$1 AND p.user_id=$2 AND p.course_id=a.course_id
 WHERE ($3::timestamptz IS NULL OR (a.created_at,a.id)<($3,$4::uuid)) ORDER BY a.created_at DESC,a.id DESC LIMIT $5`, org, user, ts, id, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MyAssignment{}
	for rows.Next() {
		var m domain.MyAssignment
		if err = rows.Scan(&m.AssignmentID, &m.Course.ID, &m.Course.Code, &m.Course.Title, &m.Course.Description, &m.Course.EstimatedDurationMinutes, &m.DueAt, &m.AssignedAt, &m.ProgressStatus, &m.CompletedAt, &m.BestScore); err != nil {
			return nil, err
		}
		m.AssignedAt = m.AssignedAt.UTC()
		if m.DueAt != nil {
			due := m.DueAt.UTC()
			m.DueAt = &due
		}
		if m.CompletedAt != nil {
			done := m.CompletedAt.UTC()
			m.CompletedAt = &done
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *store) AssignedCourseIDs(ctx context.Context, org, user string, limit int) ([]string, error) {
	rows, err := s.tx.Query(ctx, `WITH applicable AS (`+applicableAssignments+`)
 SELECT c.id::text FROM applicable a JOIN training_courses c ON c.organization_id=$1 AND c.id=a.course_id AND c.status='PUBLISHED' ORDER BY a.created_at DESC,a.id LIMIT $3`, org, user, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
