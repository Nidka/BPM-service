package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
)

// EnsureAchievements materializa el catálogo por defecto de la plataforma en la
// organización. Es idempotente y no pisa personalizaciones (ON CONFLICT DO NOTHING).
func (s *store) EnsureAchievements(ctx context.Context, org string) error {
	batch := &pgx.Batch{}
	for _, a := range domain.DefaultAchievements() {
		batch.Queue(`INSERT INTO training_achievements(organization_id,code,name,description,points,criterion_type,criterion_threshold,active)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (organization_id,code) DO NOTHING`, org, a.Code, a.Name, a.Description, a.Points, a.Criterion, a.Threshold, a.Active)
	}
	br := s.tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

const achievementColumns = `a.id::text,a.code,a.name,COALESCE(a.description,''),a.points,COALESCE(a.criterion_type,''),a.criterion_threshold,a.active,ua.awarded_at`

func scanAchievement(row pgx.Row) (a domain.Achievement, err error) {
	err = row.Scan(&a.ID, &a.Code, &a.Name, &a.Description, &a.Points, &a.Criterion, &a.Threshold, &a.Active, &a.AwardedAt)
	if a.AwardedAt != nil {
		t := a.AwardedAt.UTC()
		a.AwardedAt = &t
	}
	return
}

func (s *store) queryAchievements(ctx context.Context, query string, args ...any) ([]domain.Achievement, error) {
	rows, err := s.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Achievement{}
	for rows.Next() {
		a, e := scanAchievement(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) Achievements(ctx context.Context, org, user string) ([]domain.Achievement, error) {
	return s.queryAchievements(ctx, `SELECT `+achievementColumns+` FROM training_achievements a
 LEFT JOIN user_achievements ua ON ua.organization_id=a.organization_id AND ua.achievement_id=a.id AND ua.user_id=$2
 WHERE a.organization_id=$1 ORDER BY a.criterion_type,a.criterion_threshold,a.code`, org, user)
}

func (s *store) AttemptAchievements(ctx context.Context, org, attemptID string) ([]domain.Achievement, error) {
	return s.queryAchievements(ctx, `SELECT `+achievementColumns+` FROM user_achievements ua
 JOIN training_achievements a ON a.organization_id=ua.organization_id AND a.id=ua.achievement_id
 WHERE ua.organization_id=$1 AND ua.source_attempt_id=$2 ORDER BY a.code`, org, attemptID)
}

func (s *store) AchievementStats(ctx context.Context, org, user string) (domain.AchievementStats, error) {
	var st domain.AchievementStats
	err := s.tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM training_progress WHERE organization_id=$1 AND user_id=$2 AND status='COMPLETED'),
 (SELECT count(*) FROM training_attempts WHERE organization_id=$1 AND user_id=$2 AND attempt_type IN ('FINAL','MODULE') AND score=100)`, org, user).Scan(&st.CoursesCompleted, &st.PerfectAttempts)
	if err != nil {
		return st, err
	}
	rows, err := s.tx.Query(ctx, `SELECT DISTINCT (completed_at AT TIME ZONE 'UTC')::date AS d FROM training_attempts WHERE organization_id=$1 AND user_id=$2 AND completed_at IS NOT NULL ORDER BY d DESC LIMIT 60`, org, user)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err = rows.Scan(&d); err != nil {
			return st, err
		}
		days = append(days, d)
	}
	st.StreakDays = domain.StreakDays(days)
	return st, rows.Err()
}

func (s *store) GrantAchievement(ctx context.Context, org, user, achievementID, attemptID string) (bool, error) {
	tag, err := s.tx.Exec(ctx, `INSERT INTO user_achievements(organization_id,user_id,achievement_id,source_attempt_id,created_by,updated_by)
 VALUES($1,$2,$3,$4,$2,$2) ON CONFLICT (organization_id,user_id,achievement_id) DO NOTHING`, org, user, achievementID, attemptID)
	return tag.RowsAffected() == 1, err
}

func (s *store) UpdateAchievement(ctx context.Context, org, user, id string, u domain.AchievementUpdate) (domain.Achievement, error) {
	a, err := scanAchievement(s.tx.QueryRow(ctx, `WITH changed AS (UPDATE training_achievements SET name=$3,description=NULLIF($4,''),points=$5,criterion_threshold=$6,active=$7,updated_by=$8
 WHERE organization_id=$1 AND id=$2 RETURNING *)
 SELECT `+achievementColumns+` FROM changed a LEFT JOIN user_achievements ua ON false`, org, id, u.Name, u.Description, u.Points, u.Threshold, u.Active, user))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrAchievementNotFound
	}
	return a, err
}

func (s *store) XPTotals(ctx context.Context, org, user string) (domain.XPTotals, error) {
	var x domain.XPTotals
	err := s.tx.QueryRow(ctx, `SELECT
 COALESCE((SELECT sum(experience_points) FROM training_progress WHERE organization_id=$1 AND user_id=$2),0),
 COALESCE((SELECT sum(experience_points) FROM training_module_results WHERE organization_id=$1 AND user_id=$2),0),
 COALESCE((SELECT sum(a.points) FROM user_achievements ua JOIN training_achievements a ON a.organization_id=ua.organization_id AND a.id=ua.achievement_id WHERE ua.organization_id=$1 AND ua.user_id=$2),0)`, org, user).Scan(&x.Course, &x.Module, &x.Achievements)
	return x, err
}

func (s *store) SaveModuleResult(ctx context.Context, org, user, courseID, lessonID string, score int, passed bool, xp int) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO training_module_results(organization_id,user_id,course_id,lesson_id,best_score,passed,experience_points,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$2,$2)
 ON CONFLICT (organization_id,user_id,lesson_id) DO UPDATE SET best_score=GREATEST(training_module_results.best_score,EXCLUDED.best_score),
 passed=training_module_results.passed OR EXCLUDED.passed,experience_points=GREATEST(training_module_results.experience_points,EXCLUDED.experience_points),updated_by=EXCLUDED.updated_by`,
		org, user, courseID, lessonID, score, passed, xp)
	return err
}

func (s *store) ModuleXP(ctx context.Context, org, user, lessonID string) (int, error) {
	var xp int
	err := s.tx.QueryRow(ctx, `SELECT experience_points FROM training_module_results WHERE organization_id=$1 AND user_id=$2 AND lesson_id=$3`, org, user, lessonID).Scan(&xp)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return xp, err
}

// LearningActivity une los intentos calificados y las lecciones completadas; UNION descarta instantes repetidos.
func (s *store) LearningActivity(ctx context.Context, org, user string, since time.Time) ([]time.Time, error) {
	rows, err := s.tx.Query(ctx, `SELECT completed_at FROM training_attempts WHERE organization_id=$1 AND user_id=$2 AND completed_at IS NOT NULL AND completed_at>=$3
 UNION SELECT completed_at FROM training_lesson_progress WHERE organization_id=$1 AND user_id=$2 AND completed_at>=$3`, org, user, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []time.Time{}
	for rows.Next() {
		var at time.Time
		if err = rows.Scan(&at); err != nil {
			return nil, err
		}
		out = append(out, at.UTC())
	}
	return out, rows.Err()
}

func (s *store) UserTimezone(ctx context.Context, org, user string) (string, error) {
	var zone string
	err := s.tx.QueryRow(ctx, `SELECT l.timezone FROM membership_locations ml
 JOIN locations l ON l.organization_id=ml.organization_id AND l.id=ml.location_id AND l.status='ACTIVE'
 WHERE ml.organization_id=$1 AND ml.user_id=$2 ORDER BY ml.created_at,l.name,l.id LIMIT 1`, org, user).Scan(&zone)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return zone, err
}
