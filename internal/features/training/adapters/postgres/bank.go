package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
)

// drawItem es la forma guardada de un ítem sorteado en training_evaluation_draws.items.
type drawItem struct {
	QuestionID  string `json:"questionId"`
	ItemCode    string `json:"itemCode"`
	OptionOrder []int  `json:"optionOrder"`
}

func (s *store) FindDraw(ctx context.Context, org, id string) (domain.Draw, bool, error) {
	var d domain.Draw
	var raw []byte
	err := s.tx.QueryRow(ctx, `SELECT id::text,user_id,course_version_id::text,attempt_type,bank_version,attempt_number,items,created_at
 FROM training_evaluation_draws WHERE organization_id=$1 AND id=$2`, org, id).Scan(&d.ID, &d.UserID, &d.VersionID, &d.AttemptType, &d.BankVersion, &d.AttemptNumber, &raw, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	var items []drawItem
	if err = json.Unmarshal(raw, &items); err != nil {
		return d, false, err
	}
	for _, it := range items {
		d.Items = append(d.Items, domain.DrawItem{QuestionID: it.QuestionID, ItemCode: it.ItemCode, OptionOrder: it.OptionOrder})
	}
	d.CreatedAt = d.CreatedAt.UTC()
	return d, true, nil
}

func (s *store) InsertDraw(ctx context.Context, org string, d domain.Draw) error {
	items := make([]drawItem, len(d.Items))
	for i, it := range d.Items {
		items[i] = drawItem{QuestionID: it.QuestionID, ItemCode: it.ItemCode, OptionOrder: it.OptionOrder}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.tx.Exec(ctx, `INSERT INTO training_evaluation_draws(id,organization_id,user_id,course_version_id,attempt_type,bank_version,attempt_number,items,created_at,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$3,$3)`, d.ID, org, d.UserID, d.VersionID, d.AttemptType, d.BankVersion, d.AttemptNumber, string(raw), d.CreatedAt)
	if isUniqueViolation(err) {
		return domain.ErrIdempotencyConflict
	}
	return err
}

// ItemHistory cuenta los ítems de las evaluaciones terminadas de la persona en el curso (todas las versiones).
func (s *store) ItemHistory(ctx context.Context, org, user, courseID string) ([]domain.ItemUse, error) {
	rows, err := s.tx.Query(ctx, `SELECT a.item_code,count(*),max(t.completed_at)
 FROM training_attempt_answers a
 JOIN training_attempts t ON t.organization_id=a.organization_id AND t.id=a.attempt_id
 JOIN training_course_versions v ON v.organization_id=t.organization_id AND v.id=t.course_version_id
 WHERE a.organization_id=$1 AND t.user_id=$2 AND v.course_id=$3 AND a.item_code IS NOT NULL
 GROUP BY a.item_code ORDER BY a.item_code`, org, user, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ItemUse{}
	for rows.Next() {
		var u domain.ItemUse
		var last *time.Time
		if err = rows.Scan(&u.ItemCode, &u.Uses, &last); err != nil {
			return nil, err
		}
		if last != nil {
			u.LastUsed = last.UTC()
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *store) RecordPractice(ctx context.Context, org, user, courseID, itemCode string, correct bool, now time.Time) (int, error) {
	prev := domain.PracticeState{}
	err := s.tx.QueryRow(ctx, `SELECT answered_count,correct_count,box,experience_points,last_correct,due_at FROM training_practice_results
 WHERE organization_id=$1 AND user_id=$2 AND course_id=$3 AND item_code=$4 FOR UPDATE`, org, user, courseID, itemCode).Scan(&prev.Answered, &prev.Correct, &prev.Box, &prev.XP, &prev.LastCorrect, &prev.DueAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	next, gained := domain.NextPractice(prev, correct, now)
	_, err = s.tx.Exec(ctx, `INSERT INTO training_practice_results(organization_id,user_id,course_id,item_code,answered_count,correct_count,last_correct,box,due_at,experience_points,last_answered_at,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$2,$2)
 ON CONFLICT (organization_id,user_id,course_id,item_code) DO UPDATE SET answered_count=EXCLUDED.answered_count,correct_count=EXCLUDED.correct_count,last_correct=EXCLUDED.last_correct,
 box=EXCLUDED.box,due_at=EXCLUDED.due_at,experience_points=EXCLUDED.experience_points,last_answered_at=EXCLUDED.last_answered_at,updated_by=EXCLUDED.updated_by`,
		org, user, courseID, itemCode, next.Answered, next.Correct, next.LastCorrect, next.Box, next.DueAt, next.XP, now)
	return gained, err
}

// ReviewDue lista las preguntas de la versión con repaso pendiente: las falladas primero y luego las vencidas.
func (s *store) ReviewDue(ctx context.Context, org, user, courseID, versionID string, now time.Time) (domain.ReviewInfo, error) {
	rows, err := s.tx.Query(ctx, `SELECT q.id::text FROM training_practice_results p
 JOIN training_questions q ON q.organization_id=p.organization_id AND q.course_version_id=$4 AND q.item_code=p.item_code
 WHERE p.organization_id=$1 AND p.user_id=$2 AND p.course_id=$3 AND p.due_at IS NOT NULL AND p.due_at<=$5
 ORDER BY p.due_at,q.display_order`, org, user, courseID, versionID, now)
	if err != nil {
		return domain.ReviewInfo{}, err
	}
	defer rows.Close()
	info := domain.ReviewInfo{QuestionIDs: []string{}}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return info, err
		}
		info.DueCount++
		if len(info.QuestionIDs) < domain.ReviewBatch {
			info.QuestionIDs = append(info.QuestionIDs, id)
		}
	}
	return info, rows.Err()
}

func (s *store) CourseByCode(ctx context.Context, org, code string) (domain.Course, bool, error) {
	var c domain.Course
	err := s.tx.QueryRow(ctx, `SELECT id::text,code,title,COALESCE(description,''),estimated_duration_minutes,status FROM training_courses WHERE organization_id=$1 AND code=$2`, org, code).
		Scan(&c.ID, &c.Code, &c.Title, &c.Description, &c.EstimatedDurationMinutes, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *store) PublishedBankVersion(ctx context.Context, org, courseID string) (int, error) {
	var version int
	err := s.tx.QueryRow(ctx, `SELECT COALESCE(max(q.bank_version),0) FROM training_course_versions v
 LEFT JOIN training_questions q ON q.organization_id=v.organization_id AND q.course_version_id=v.id
 WHERE v.organization_id=$1 AND v.course_id=$2 AND v.status='PUBLISHED'`, org, courseID).Scan(&version)
	return version, err
}
