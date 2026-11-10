package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
)

func decodeOptions(raw []byte) ([]string, error) {
	var options []string
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, err
	}
	return options, nil
}

// CourseView lee la versión PUBLISHED de un curso PUBLISHED sin seleccionar
// respuestas correctas ni explicaciones.
func (s *store) CourseView(ctx context.Context, org, userID, courseID string) (domain.CourseView, error) {
	var view domain.CourseView
	err := s.tx.QueryRow(ctx, `SELECT c.id::text,c.code,c.title,COALESCE(c.description,''),c.estimated_duration_minutes,c.status,v.id::text,v.version_number,v.passing_score,v.published_at
 FROM training_courses c JOIN training_course_versions v ON v.organization_id=c.organization_id AND v.course_id=c.id AND v.status='PUBLISHED'
 WHERE c.organization_id=$1 AND c.id=$2 AND c.status='PUBLISHED'`, org, courseID).Scan(
		&view.Course.ID, &view.Course.Code, &view.Course.Title, &view.Course.Description, &view.Course.EstimatedDurationMinutes, &view.Course.Status,
		&view.Version.ID, &view.Version.Number, &view.Version.PassingScore, &view.Version.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, domain.ErrCourseNotFound
	}
	if err != nil {
		return view, err
	}
	rows, err := s.tx.Query(ctx, `SELECT l.id::text,l.title,l.content,l.display_order,m.id::text,m.mime_type,m.size_bytes
 FROM training_lessons l LEFT JOIN media_files m ON m.organization_id=l.organization_id AND m.id=l.media_file_id AND m.status='AVAILABLE' AND m.location_id IS NULL AND starts_with(m.object_key,$3)
 WHERE l.organization_id=$1 AND l.course_version_id=$2 ORDER BY l.display_order`, org, view.Version.ID, trainingVideoPrefix(org))
	if err != nil {
		return view, err
	}
	defer rows.Close()
	view.Lessons = []domain.LearnLesson{}
	for rows.Next() {
		var lesson domain.LearnLesson
		var mediaID, mime *string
		var size *int64
		if err = rows.Scan(&lesson.ID, &lesson.Title, &lesson.Content, &lesson.DisplayOrder, &mediaID, &mime, &size); err != nil {
			return view, err
		}
		if mediaID != nil && mime != nil && size != nil {
			lesson.Video = &domain.Video{MediaID: *mediaID, MIMEType: *mime, SizeBytes: *size}
		}
		view.Lessons = append(view.Lessons, lesson)
	}
	if err = rows.Err(); err != nil {
		return view, err
	}
	rows.Close()
	qrows, err := s.tx.Query(ctx, `SELECT id::text,COALESCE(lesson_id::text,''),question_text,options,display_order FROM training_questions WHERE organization_id=$1 AND course_version_id=$2 ORDER BY display_order`, org, view.Version.ID)
	if err != nil {
		return view, err
	}
	defer qrows.Close()
	view.Questions = []domain.PublicQuestion{}
	for qrows.Next() {
		var q domain.PublicQuestion
		var raw []byte
		if err = qrows.Scan(&q.ID, &q.LessonID, &q.Text, &raw, &q.DisplayOrder); err != nil {
			return view, err
		}
		if q.Options, err = decodeOptions(raw); err != nil {
			return view, err
		}
		view.Questions = append(view.Questions, q)
	}
	if err = qrows.Err(); err != nil {
		return view, err
	}
	qrows.Close()
	view.Progress = domain.Progress{Status: domain.ProgressNotStarted}
	if userID != "" {
		if view.Progress, err = s.Progress(ctx, org, userID, courseID); err != nil {
			return view, err
		}
		if view.Certificate, err = s.LatestCertificate(ctx, org, userID, courseID); err != nil {
			return view, err
		}
	}
	return view, nil
}

func (s *store) PublishedVersion(ctx context.Context, org, courseID string) (domain.Version, error) {
	var v domain.Version
	err := s.tx.QueryRow(ctx, `SELECT v.id::text,v.version_number,v.passing_score,v.published_at FROM training_course_versions v
 JOIN training_courses c ON c.organization_id=v.organization_id AND c.id=v.course_id AND c.status='PUBLISHED'
 WHERE v.organization_id=$1 AND v.course_id=$2 AND v.status='PUBLISHED'`, org, courseID).Scan(&v.ID, &v.Number, &v.PassingScore, &v.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, domain.ErrCourseNotFound
	}
	return v, err
}

func (s *store) LessonInVersion(ctx context.Context, org, versionID, lessonID string) error {
	var found string
	err := s.tx.QueryRow(ctx, `SELECT id::text FROM training_lessons WHERE organization_id=$1 AND course_version_id=$2 AND id=$3`, org, versionID, lessonID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrLessonNotFound
	}
	return err
}

const videoColumns = `m.id::text,m.organization_id,m.object_key,m.original_name,m.mime_type,m.size_bytes,COALESCE(m.sha256,''),m.status,COALESCE(m.created_by,''),m.created_at,m.uploaded_at`

func scanVideo(row pgx.Row) (f domain.VideoFile, err error) {
	err = row.Scan(&f.ID, &f.OrganizationID, &f.ObjectKey, &f.OriginalName, &f.MIMEType, &f.SizeBytes, &f.SHA256, &f.Status, &f.CreatedBy, &f.CreatedAt, &f.UploadedAt)
	return
}

func (s *store) LessonVideo(ctx context.Context, org, versionID, lessonID string) (domain.VideoFile, error) {
	file, err := scanVideo(s.tx.QueryRow(ctx, `SELECT `+videoColumns+` FROM training_lessons l JOIN media_files m ON m.organization_id=l.organization_id AND m.id=l.media_file_id
 WHERE l.organization_id=$1 AND l.course_version_id=$2 AND l.id=$3 AND m.status='AVAILABLE' AND m.location_id IS NULL AND starts_with(m.object_key,$4)`, org, versionID, lessonID, trainingVideoPrefix(org)))
	if errors.Is(err, pgx.ErrNoRows) {
		return file, domain.ErrVideoNotFound
	}
	return file, err
}

const questionColumns = `id::text,COALESCE(lesson_id::text,''),question_text,options,correct_option,COALESCE(explanation,''),display_order`

func scanQuestion(row pgx.Row) (q domain.Question, err error) {
	var raw []byte
	if err = row.Scan(&q.ID, &q.LessonID, &q.Text, &raw, &q.CorrectOption, &q.Explanation, &q.DisplayOrder); err != nil {
		return q, err
	}
	q.Options, err = decodeOptions(raw)
	return q, err
}

func (s *store) Question(ctx context.Context, org, versionID, questionID string) (domain.Question, error) {
	q, err := scanQuestion(s.tx.QueryRow(ctx, `SELECT `+questionColumns+` FROM training_questions WHERE organization_id=$1 AND course_version_id=$2 AND id=$3`, org, versionID, questionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return q, domain.ErrQuestionNotFound
	}
	return q, err
}

func (s *store) Questions(ctx context.Context, org, versionID string) ([]domain.Question, error) {
	rows, err := s.tx.Query(ctx, `SELECT `+questionColumns+` FROM training_questions WHERE organization_id=$1 AND course_version_id=$2 ORDER BY display_order`, org, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Question{}
	for rows.Next() {
		q, e := scanQuestion(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *store) LockLearner(ctx context.Context, org, user, courseID string) error {
	_, err := s.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('training:' || $1 || ':' || $2 || ':' || $3, 0))`, org, user, courseID)
	return err
}

func (s *store) Progress(ctx context.Context, org, user, courseID string) (domain.Progress, error) {
	p := domain.Progress{Status: domain.ProgressNotStarted}
	err := s.tx.QueryRow(ctx, `SELECT status,COALESCE(last_lesson_id::text,''),best_score,experience_points,completed_at FROM training_progress WHERE organization_id=$1 AND user_id=$2 AND course_id=$3`, org, user, courseID).Scan(&p.Status, &p.LastLessonID, &p.BestScore, &p.ExperiencePoints, &p.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Progress{Status: domain.ProgressNotStarted}, nil
	}
	return p, err
}

func (s *store) SaveProgress(ctx context.Context, org, user, courseID string, p domain.Progress) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO training_progress(organization_id,user_id,course_id,last_lesson_id,status,best_score,experience_points,completed_at,created_by,updated_by)
 VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$2,$2)
 ON CONFLICT (organization_id,user_id,course_id) DO UPDATE SET last_lesson_id=EXCLUDED.last_lesson_id,status=EXCLUDED.status,best_score=EXCLUDED.best_score,experience_points=EXCLUDED.experience_points,completed_at=EXCLUDED.completed_at,updated_by=EXCLUDED.updated_by`,
		org, user, courseID, p.LastLessonID, p.Status, p.BestScore, p.ExperiencePoints, p.CompletedAt)
	return err
}

func (s *store) FindAttempt(ctx context.Context, org, attemptID, operationID string) ([]application.AttemptRecord, error) {
	rows, err := s.tx.Query(ctx, `SELECT a.id::text,a.client_operation_id::text,v.course_id::text,a.course_version_id::text,a.user_id,a.attempt_type,COALESCE(a.request_hash,''),COALESCE(a.score,0),COALESCE(a.passed,false),a.started_at,a.client_created_at,COALESCE(a.completed_at,a.created_at)
 FROM training_attempts a JOIN training_course_versions v ON v.organization_id=a.organization_id AND v.id=a.course_version_id
 WHERE a.organization_id=$1 AND (a.id=$2 OR a.client_operation_id=$3)`, org, attemptID, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []application.AttemptRecord
	for rows.Next() {
		var r application.AttemptRecord
		if err = rows.Scan(&r.ID, &r.ClientOperationID, &r.CourseID, &r.VersionID, &r.UserID, &r.AttemptType, &r.RequestHash, &r.Score, &r.Passed, &r.StartedAt, &r.ClientCreatedAt, &r.CompletedAt); err != nil {
			return nil, err
		}
		r.StartedAt, r.ClientCreatedAt, r.CompletedAt = r.StartedAt.UTC(), r.ClientCreatedAt.UTC(), r.CompletedAt.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) InsertAttempt(ctx context.Context, org string, r application.AttemptRecord, results []domain.Result) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO training_attempts(id,organization_id,course_version_id,user_id,attempt_type,score,passed,started_at,completed_at,client_operation_id,client_created_at,request_hash,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$4,$4)`, r.ID, org, r.VersionID, r.UserID, r.AttemptType, r.Score, r.Passed, r.StartedAt, r.CompletedAt, r.ClientOperationID, r.ClientCreatedAt, r.RequestHash)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrIdempotencyConflict
		}
		return err
	}
	batch := &pgx.Batch{}
	for _, result := range results {
		batch.Queue(`INSERT INTO training_attempt_answers(organization_id,attempt_id,question_id,selected_option,is_correct,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$6)`, org, r.ID, result.QuestionID, result.SelectedOption, result.Correct, r.UserID)
	}
	if batch.Len() == 0 {
		return nil
	}
	br := s.tx.SendBatch(ctx, batch)
	defer br.Close()
	for range results {
		if _, err = br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

func (s *store) AttemptResults(ctx context.Context, org, attemptID string) ([]domain.Result, error) {
	rows, err := s.tx.Query(ctx, `SELECT a.question_id::text,a.selected_option,a.is_correct,q.correct_option,COALESCE(q.explanation,'')
 FROM training_attempt_answers a JOIN training_questions q ON q.organization_id=a.organization_id AND q.id=a.question_id
 WHERE a.organization_id=$1 AND a.attempt_id=$2 ORDER BY q.display_order`, org, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Result{}
	for rows.Next() {
		var r domain.Result
		if err = rows.Scan(&r.QuestionID, &r.SelectedOption, &r.Correct, &r.CorrectOption, &r.Explanation); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) IssueCertificate(ctx context.Context, org, user, versionID, attemptID, code string) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO training_certificates(organization_id,user_id,course_version_id,final_attempt_id,verification_code,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$2,$2) ON CONFLICT (organization_id,user_id,course_version_id) DO NOTHING`, org, user, versionID, attemptID, code)
	return err
}

func (s *store) LatestCertificate(ctx context.Context, org, user, courseID string) (*domain.Certificate, error) {
	var c domain.Certificate
	var issued time.Time
	err := s.tx.QueryRow(ctx, `SELECT c.id::text,c.verification_code,c.issued_at FROM training_certificates c
 JOIN training_course_versions v ON v.organization_id=c.organization_id AND v.id=c.course_version_id
 WHERE c.organization_id=$1 AND c.user_id=$2 AND v.course_id=$3 AND c.revoked_at IS NULL ORDER BY c.issued_at DESC,c.id LIMIT 1`, org, user, courseID).Scan(&c.ID, &c.VerificationCode, &issued)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	c.IssuedAt = issued.UTC()
	return &c, err
}
