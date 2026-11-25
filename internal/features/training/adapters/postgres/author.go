package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
)

func (s *store) ListCourses(ctx context.Context, org, status string) ([]domain.CourseSummary, error) {
	rows, err := s.tx.Query(ctx, `SELECT c.id::text,c.code,c.title,c.status,
 (SELECT v.version_number FROM training_course_versions v WHERE v.organization_id=c.organization_id AND v.course_id=c.id AND v.status='PUBLISHED'),
 EXISTS(SELECT 1 FROM training_course_versions v WHERE v.organization_id=c.organization_id AND v.course_id=c.id AND v.status='DRAFT')
 FROM training_courses c WHERE c.organization_id=$1 AND ($2::text='' OR c.status=$2) ORDER BY c.title,c.id LIMIT 100`, org, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CourseSummary{}
	for rows.Next() {
		var c domain.CourseSummary
		if err = rows.Scan(&c.ID, &c.Code, &c.Title, &c.Status, &c.PublishedVersionNumber, &c.HasDraft); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) CreateCourse(ctx context.Context, org, user string, in domain.NewCourse) (domain.Course, error) {
	var c domain.Course
	err := s.tx.QueryRow(ctx, `INSERT INTO training_courses(organization_id,code,title,description,estimated_duration_minutes,status,created_by,updated_by)
 VALUES($1,$2,$3,NULLIF($4,''),$5,'DRAFT',$6,$6) RETURNING id::text,code,title,COALESCE(description,''),estimated_duration_minutes,status`,
		org, in.Code, in.Title, in.Description, in.EstimatedDurationMinutes, user).Scan(&c.ID, &c.Code, &c.Title, &c.Description, &c.EstimatedDurationMinutes, &c.Status)
	if isUniqueViolation(err) {
		return domain.Course{}, domain.ErrCourseCodeTaken
	}
	return c, err
}

func (s *store) LockCourse(ctx context.Context, org, courseID string) (domain.Course, error) {
	var c domain.Course
	err := s.tx.QueryRow(ctx, `SELECT id::text,code,title,COALESCE(description,''),estimated_duration_minutes,status FROM training_courses WHERE organization_id=$1 AND id=$2 FOR UPDATE`, org, courseID).Scan(&c.ID, &c.Code, &c.Title, &c.Description, &c.EstimatedDurationMinutes, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrCourseNotFound
	}
	return c, err
}

func (s *store) Draft(ctx context.Context, org string, course domain.Course) (domain.Draft, bool, error) {
	draft := domain.Draft{Course: course}
	err := s.tx.QueryRow(ctx, `SELECT id::text,version_number,passing_score,row_version FROM training_course_versions WHERE organization_id=$1 AND course_id=$2 AND status='DRAFT' FOR UPDATE`, org, course.ID).Scan(&draft.Version.ID, &draft.Version.Number, &draft.Version.PassingScore, &draft.Version.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return draft, false, nil
	}
	if err != nil {
		return draft, false, err
	}
	draft.Lessons, draft.Questions, err = s.content(ctx, org, draft.Version.ID)
	return draft, err == nil, err
}

// content lee lecciones y preguntas (con respuestas) de una versión de autoría.
func (s *store) content(ctx context.Context, org, versionID string) ([]domain.Lesson, []domain.Question, error) {
	rows, err := s.tx.Query(ctx, `SELECT id::text,title,content,display_order,COALESCE(media_file_id::text,''),external_video_provider,external_video_id,key_points,captions_vtt FROM training_lessons WHERE organization_id=$1 AND course_version_id=$2 ORDER BY display_order`, org, versionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	lessons := []domain.Lesson{}
	for rows.Next() {
		var l domain.Lesson
		var provider, externalID *string
		var keyPoints []byte
		var captions string
		if err = rows.Scan(&l.ID, &l.Title, &l.Content, &l.DisplayOrder, &l.VideoMediaID, &provider, &externalID, &keyPoints, &captions); err != nil {
			return nil, nil, err
		}
		if l.KeyPoints, err = decodeKeyPoints(keyPoints); err != nil {
			return nil, nil, err
		}
		l.Captions = &captions
		if provider != nil && externalID != nil {
			l.ExternalVideo = &domain.ExternalVideo{Provider: *provider, VideoID: *externalID}
		}
		lessons = append(lessons, l)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()
	questions, err := s.Questions(ctx, org, versionID)
	return lessons, questions, err
}

func (s *store) CreateDraft(ctx context.Context, org, user string, course domain.Course) (domain.Draft, error) {
	var next int
	if err := s.tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_number),0)+1 FROM training_course_versions WHERE organization_id=$1 AND course_id=$2`, org, course.ID).Scan(&next); err != nil {
		return domain.Draft{}, err
	}
	score := domain.DefaultPassScore
	in := domain.DraftInput{}
	var publishedID string
	err := s.tx.QueryRow(ctx, `SELECT id::text,passing_score FROM training_course_versions WHERE organization_id=$1 AND course_id=$2 AND status='PUBLISHED'`, org, course.ID).Scan(&publishedID, &score)
	switch {
	case err == nil:
		lessons, questions, e := s.content(ctx, org, publishedID)
		if e != nil {
			return domain.Draft{}, e
		}
		if in.Lessons, in.Questions, e = cloneContent(lessons, questions); e != nil {
			return domain.Draft{}, e
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return domain.Draft{}, err
	}
	var draft domain.Draft
	draft.Course = course
	err = s.tx.QueryRow(ctx, `INSERT INTO training_course_versions(organization_id,course_id,version_number,passing_score,status,created_by,updated_by) VALUES($1,$2,$3,$4,'DRAFT',$5,$5) RETURNING id::text,version_number,passing_score,row_version`, org, course.ID, next, score, user).Scan(&draft.Version.ID, &draft.Version.Number, &draft.Version.PassingScore, &draft.Version.RowVersion)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Draft{}, domain.ErrDraftConflict
		}
		return domain.Draft{}, err
	}
	if err = s.insertContent(ctx, org, user, draft.Version.ID, in.Lessons, in.Questions); err != nil {
		return domain.Draft{}, err
	}
	draft.Lessons, draft.Questions = in.Lessons, in.Questions
	return draft, nil
}

// cloneContent asigna identificadores nuevos conservando la relación pregunta-lección.
func cloneContent(lessons []domain.Lesson, questions []domain.Question) ([]domain.Lesson, []domain.Question, error) {
	mapping := make(map[string]string, len(lessons))
	outLessons := make([]domain.Lesson, len(lessons))
	for i, l := range lessons {
		id, err := newUUID()
		if err != nil {
			return nil, nil, err
		}
		mapping[l.ID] = id
		l.ID = id
		outLessons[i] = l
	}
	outQuestions := make([]domain.Question, len(questions))
	for i, q := range questions {
		id, err := newUUID()
		if err != nil {
			return nil, nil, err
		}
		q.ID = id
		if q.LessonID != "" {
			q.LessonID = mapping[q.LessonID]
		}
		outQuestions[i] = q
	}
	return outLessons, outQuestions, nil
}

// captionText devuelve los subtítulos a guardar ("" si la lección no tiene).
func captionText(l domain.Lesson) string {
	if l.Captions == nil {
		return ""
	}
	return *l.Captions
}

// nonNilPoints garantiza que key_points se guarde como arreglo JSON ([] y no null).
func nonNilPoints(points []string) []string {
	if points == nil {
		return []string{}
	}
	return points
}

func (s *store) insertContent(ctx context.Context, org, user, versionID string, lessons []domain.Lesson, questions []domain.Question) error {
	batch := &pgx.Batch{}
	for _, l := range lessons {
		points, err := json.Marshal(nonNilPoints(l.KeyPoints))
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO training_lessons(id,organization_id,course_version_id,title,content,display_order,media_file_id,external_video_provider,external_video_id,key_points,captions_vtt,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10::jsonb,$12,$11,$11)`,
			l.ID, org, versionID, l.Title, l.Content, l.DisplayOrder, l.VideoMediaID, externalProvider(l), externalID(l), string(points), user, captionText(l))
	}
	for _, q := range questions {
		options, err := json.Marshal(q.Options)
		if err != nil {
			return err
		}
		sources, err := json.Marshal(nonNilPoints(q.Sources))
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO training_questions(id,organization_id,course_version_id,lesson_id,question_text,options,correct_option,explanation,display_order,item_code,bank_version,bloom_level,source_refs,created_by,updated_by) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,$6::jsonb,$7,NULLIF($8,''),$9,NULLIF($10,''),NULLIF($11,0),NULLIF($12,''),$13::jsonb,$14,$14)`,
			q.ID, org, versionID, q.LessonID, q.Text, string(options), q.CorrectOption, q.Explanation, q.DisplayOrder, q.ItemCode, q.BankVersion, q.Bloom, string(sources), user)
	}
	if batch.Len() == 0 {
		return nil
	}
	br := s.tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			if isUniqueViolation(err) {
				return domain.ErrDraftConflict
			}
			return err
		}
	}
	return br.Close()
}

func (s *store) ReplaceDraft(ctx context.Context, org, user string, draft domain.Draft, in domain.DraftInput) (domain.Draft, error) {
	tag, err := s.tx.Exec(ctx, `UPDATE training_course_versions SET passing_score=$4,row_version=row_version+1,updated_by=$5 WHERE organization_id=$1 AND id=$2 AND status='DRAFT' AND row_version=$3`, org, draft.Version.ID, draft.Version.RowVersion, in.PassingScore, user)
	if err != nil {
		return draft, err
	}
	if tag.RowsAffected() != 1 {
		return draft, domain.ErrDraftConflict
	}
	for _, query := range []string{`DELETE FROM training_questions WHERE organization_id=$1 AND course_version_id=$2`, `DELETE FROM training_lessons WHERE organization_id=$1 AND course_version_id=$2`} {
		if _, err = s.tx.Exec(ctx, query, org, draft.Version.ID); err != nil {
			return draft, err
		}
	}
	if err = s.insertContent(ctx, org, user, draft.Version.ID, in.Lessons, in.Questions); err != nil {
		return draft, err
	}
	draft.Version.PassingScore = in.PassingScore
	draft.Version.RowVersion++
	draft.Lessons, draft.Questions = in.Lessons, in.Questions
	return draft, nil
}

func (s *store) VideoStatuses(ctx context.Context, org string, ids []string) (map[string]string, error) {
	rows, err := s.tx.Query(ctx, `SELECT id::text,status FROM media_files WHERE organization_id=$1 AND id::text=ANY($2::text[]) AND location_id IS NULL AND starts_with(object_key,$3)`, org, ids, trainingVideoPrefix(org))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, rows.Err()
}

func (s *store) PublishDraft(ctx context.Context, org, user string, draft domain.Draft, now time.Time) (domain.Course, domain.Version, error) {
	course := draft.Course
	// Primero se archiva la versión vigente: el índice único admite una sola PUBLISHED por curso.
	if _, err := s.tx.Exec(ctx, `UPDATE training_course_versions SET status='ARCHIVED',updated_by=$3 WHERE organization_id=$1 AND course_id=$2 AND status='PUBLISHED'`, org, course.ID, user); err != nil {
		return course, draft.Version, err
	}
	version := draft.Version
	err := s.tx.QueryRow(ctx, `UPDATE training_course_versions SET status='PUBLISHED',published_at=$3,row_version=row_version+1,updated_by=$4 WHERE organization_id=$1 AND id=$2 AND status='DRAFT' RETURNING row_version,published_at`, org, version.ID, now, user).Scan(&version.RowVersion, &version.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return course, version, domain.ErrDraftConflict
	}
	if err != nil {
		return course, version, err
	}
	if _, err = s.tx.Exec(ctx, `UPDATE training_courses SET status='PUBLISHED',updated_by=$3 WHERE organization_id=$1 AND id=$2`, org, course.ID, user); err != nil {
		return course, version, err
	}
	course.Status = domain.StatusPublished
	return course, version, nil
}
