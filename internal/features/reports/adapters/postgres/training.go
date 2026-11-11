package postgres

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// activeMembers son las personas con membresía y cuenta activas: la población "asignable".
const activeMembers = `SELECT m.user_id FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE' WHERE m.organization_id=$1 AND m.status='ACTIVE'`

// TrainingKPI resume el avance de capacitación sobre los cursos publicados.
func (s *store) TrainingKPI(ctx context.Context, org string) (domain.TrainingKPI, error) {
	var k domain.TrainingKPI
	err := s.tx.QueryRow(ctx, `WITH members AS (`+activeMembers+`), courses AS (SELECT id FROM training_courses WHERE organization_id=$1 AND status='PUBLISHED')
 SELECT (SELECT count(*) FROM courses), (SELECT count(*) FROM members),
  count(*) FILTER (WHERE p.status='COMPLETED'), count(*) FILTER (WHERE p.status='IN_PROGRESS'),
  round(avg(p.best_score) FILTER (WHERE p.best_score IS NOT NULL),2)::float8
 FROM training_progress p WHERE p.organization_id=$1 AND p.course_id IN (SELECT id FROM courses) AND p.user_id IN (SELECT user_id FROM members)`, org).
		Scan(&k.PublishedCourses, &k.Learners, &k.CompletedEnrollments, &k.InProgressEnrollments, &k.AverageBestScore)
	if err != nil {
		return k, err
	}
	k.CompletionPercent = domain.CompletionPercent(k.CompletedEnrollments, k.PublishedCourses*k.Learners)
	return k, nil
}

// TrainingCourses devuelve el avance por curso publicado.
func (s *store) TrainingCourses(ctx context.Context, org string) ([]domain.TrainingCourseStat, error) {
	rows, err := s.tx.Query(ctx, `WITH members AS (`+activeMembers+`), n AS (SELECT count(*) AS c FROM members)
 SELECT c.id::text, c.code, c.title, (SELECT c FROM n), count(p.user_id),
  count(*) FILTER (WHERE p.status='IN_PROGRESS'), count(*) FILTER (WHERE p.status='COMPLETED'),
  round(avg(p.best_score) FILTER (WHERE p.best_score IS NOT NULL),2)::float8
 FROM training_courses c
 LEFT JOIN training_progress p ON p.organization_id=c.organization_id AND p.course_id=c.id AND p.user_id IN (SELECT user_id FROM members)
 WHERE c.organization_id=$1 AND c.status='PUBLISHED'
 GROUP BY c.id, c.code, c.title ORDER BY c.title, c.id`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TrainingCourseStat{}
	for rows.Next() {
		var v domain.TrainingCourseStat
		if err = rows.Scan(&v.CourseID, &v.Code, &v.Title, &v.Assignable, &v.Enrolled, &v.InProgress, &v.Completed, &v.AverageBestScore); err != nil {
			return nil, err
		}
		v.CompletionPercent = domain.CompletionPercent(v.Completed, v.Assignable)
		out = append(out, v)
	}
	return out, rows.Err()
}

// CourseExists indica si el curso publicado pertenece a la organización.
func (s *store) CourseExists(ctx context.Context, org, courseID string) (bool, error) {
	var ok bool
	err := s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM training_courses WHERE organization_id=$1 AND id=$2::uuid AND status='PUBLISHED')`, org, courseID).Scan(&ok)
	return ok, err
}

// TrainingPeople devuelve una página de personas con su avance, ordenadas por usuario.
func (s *store) TrainingPeople(ctx context.Context, org, courseID string, w pagination.Window) ([]domain.TrainingPerson, error) {
	var course, after any
	if courseID != "" {
		course = courseID
	}
	if w.After.ID != "" {
		after = w.After.ID
	}
	rows, err := s.tx.Query(ctx, `SELECT m.user_id, u.display_name, m.role,
  CASE WHEN $2::uuid IS NULL THEN (SELECT count(*) FROM training_courses pc WHERE pc.organization_id=m.organization_id AND pc.status='PUBLISHED') ELSE 1 END,
  count(*) FILTER (WHERE c.id IS NOT NULL AND p.status='COMPLETED'), count(*) FILTER (WHERE c.id IS NOT NULL AND p.status='IN_PROGRESS'),
  round(avg(p.best_score) FILTER (WHERE c.id IS NOT NULL AND p.best_score IS NOT NULL),2)::float8, max(p.updated_at) FILTER (WHERE c.id IS NOT NULL)
 FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 LEFT JOIN training_progress p ON p.organization_id=m.organization_id AND p.user_id=m.user_id AND ($2::uuid IS NULL OR p.course_id=$2::uuid)
 LEFT JOIN training_courses c ON c.organization_id=p.organization_id AND c.id=p.course_id AND c.status='PUBLISHED'
 WHERE m.organization_id=$1 AND m.status='ACTIVE' AND ($3::text IS NULL OR m.user_id>$3::text)
 GROUP BY m.organization_id, m.user_id, u.display_name, m.role ORDER BY m.user_id LIMIT $4`, org, course, after, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TrainingPerson{}
	for rows.Next() {
		var v domain.TrainingPerson
		if err = rows.Scan(&v.UserID, &v.DisplayName, &v.Role, &v.PublishedCourses, &v.Completed, &v.InProgress, &v.AverageBestScore, &v.LastActivityAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
