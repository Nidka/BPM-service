package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

const instrumentColumns = `i.id::text,i.code,i.name,COALESCE(i.description,''),i.status,i.is_cap,i.created_at`
const versionColumns = `v.id::text,v.instrument_id::text,v.status,v.version_number,v.row_version,v.published_at,v.created_at`

func scanInstrument(row pgx.Row) (i domain.Instrument, err error) {
	err = row.Scan(&i.ID, &i.Code, &i.Name, &i.Description, &i.Status, &i.IsCAP, &i.CreatedAt)
	return
}

func scanVersion(row pgx.Row) (v domain.Version, err error) {
	err = row.Scan(&v.ID, &v.InstrumentID, &v.Status, &v.Number, &v.RowVersion, &v.PublishedAt, &v.CreatedAt)
	return
}

func (s *store) questions(ctx context.Context, versionID string) ([]domain.Question, error) {
	rows, err := s.tx.Query(ctx, `SELECT id::text,section,answer_type,question_text,COALESCE(options,'[]'::jsonb),correct_option,display_order FROM survey_questions
		WHERE instrument_version_id=$1 ORDER BY display_order`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Question{}
	for rows.Next() {
		var q domain.Question
		var raw []byte
		if err = rows.Scan(&q.ID, &q.Section, &q.Type, &q.Text, &raw, &q.CorrectOption, &q.Order); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &q.Options); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *store) insertQuestions(ctx context.Context, user, versionID string, questions []domain.Question) error {
	for _, q := range questions {
		options, err := json.Marshal(q.Options)
		if err != nil {
			return err
		}
		if _, err = s.tx.Exec(ctx, `INSERT INTO survey_questions(instrument_version_id,section,question_text,answer_type,options,correct_option,display_order,created_by,updated_by)
			VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$8)`, versionID, q.Section, q.Text, q.Type, string(options), q.CorrectOption, q.Order, user); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) draftOf(ctx context.Context, instrument domain.Instrument) (domain.Draft, error) {
	v, err := scanVersion(s.tx.QueryRow(ctx, `SELECT `+versionColumns+` FROM survey_instrument_versions v WHERE v.instrument_id=$1 AND v.status='DRAFT'`, instrument.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Draft{}, domain.ErrDraftNotFound
	}
	if err != nil {
		return domain.Draft{}, err
	}
	qs, err := s.questions(ctx, v.ID)
	return domain.Draft{Instrument: instrument, Version: v, Questions: qs}, err
}

func (s *store) Draft(ctx context.Context, instrument domain.Instrument) (domain.Draft, error) {
	return s.draftOf(ctx, instrument)
}

func (s *store) CreateInstrument(ctx context.Context, user string, in domain.InstrumentInput, questions []domain.Question) (domain.Draft, error) {
	inst, err := scanInstrument(s.tx.QueryRow(ctx, `INSERT INTO survey_instruments AS i(code,name,description,status,is_cap,created_by,updated_by)
		VALUES($1,$2,NULLIF($3,''),'DRAFT',$4,$5,$5) RETURNING `+instrumentColumns, in.Code, in.Name, in.Description, in.IsCAP, user))
	if constraint, taken := uniqueViolation(err); taken {
		if constraint == "survey_instruments_one_cap_uq" {
			return domain.Draft{}, domain.ErrCAPExists
		}
		return domain.Draft{}, domain.ErrInstrumentCodeTaken
	}
	if err != nil {
		return domain.Draft{}, err
	}
	var versionID string
	if err = s.tx.QueryRow(ctx, `INSERT INTO survey_instrument_versions(instrument_id,version_number,status,created_by,updated_by) VALUES($1,1,'DRAFT',$2,$2) RETURNING id::text`, inst.ID, user).Scan(&versionID); err != nil {
		return domain.Draft{}, err
	}
	if err = s.insertQuestions(ctx, user, versionID, questions); err != nil {
		return domain.Draft{}, err
	}
	return s.draftOf(ctx, inst)
}

func (s *store) Instruments(ctx context.Context, w pagination.Window) ([]application.InstrumentSummary, error) {
	rows, err := s.tx.Query(ctx, `SELECT `+instrumentColumns+`,
		(SELECT v.version_number FROM survey_instrument_versions v WHERE v.instrument_id=i.id AND v.status='PUBLISHED'),
		EXISTS(SELECT 1 FROM survey_instrument_versions v WHERE v.instrument_id=i.id AND v.status='DRAFT')
		FROM survey_instruments i WHERE ($1='' OR (i.code,i.id::text)>($1,$2)) ORDER BY i.code,i.id LIMIT $3`, w.After.Key, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []application.InstrumentSummary{}
	for rows.Next() {
		var it application.InstrumentSummary
		i := &it.Instrument
		if err = rows.Scan(&i.ID, &i.Code, &i.Name, &i.Description, &i.Status, &i.IsCAP, &i.CreatedAt, &it.PublishedVersion, &it.HasDraft); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *store) LockInstrument(ctx context.Context, id string) (domain.Instrument, error) {
	i, err := scanInstrument(s.tx.QueryRow(ctx, `SELECT `+instrumentColumns+` FROM survey_instruments i WHERE i.id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return i, domain.ErrInstrumentNotFound
	}
	return i, err
}

func (s *store) InstrumentDetail(ctx context.Context, id string) (application.InstrumentDetail, error) {
	i, err := scanInstrument(s.tx.QueryRow(ctx, `SELECT `+instrumentColumns+` FROM survey_instruments i WHERE i.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.InstrumentDetail{}, domain.ErrInstrumentNotFound
	}
	if err != nil {
		return application.InstrumentDetail{}, err
	}
	rows, err := s.tx.Query(ctx, `SELECT `+versionColumns+` FROM survey_instrument_versions v WHERE v.instrument_id=$1 ORDER BY v.version_number DESC`, id)
	if err != nil {
		return application.InstrumentDetail{}, err
	}
	defer rows.Close()
	out := application.InstrumentDetail{Instrument: i, Versions: []domain.Version{}}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return out, err
		}
		out.Versions = append(out.Versions, v)
	}
	return out, rows.Err()
}

func (s *store) CreateDraft(ctx context.Context, user string, instrument domain.Instrument) (domain.Draft, error) {
	var versionID string
	err := s.tx.QueryRow(ctx, `INSERT INTO survey_instrument_versions(instrument_id,version_number,status,created_by,updated_by)
		SELECT $1::uuid,COALESCE(max(version_number),0)+1,'DRAFT',$2::text,$2::text FROM survey_instrument_versions WHERE instrument_id=$1 RETURNING id::text`, instrument.ID, user).Scan(&versionID)
	if err != nil {
		return domain.Draft{}, err
	}
	// Copia el contenido de la última versión publicada (IDs nuevos); la fuente no se modifica.
	_, err = s.tx.Exec(ctx, `INSERT INTO survey_questions(instrument_version_id,section,question_text,answer_type,options,correct_option,display_order,created_by,updated_by)
		SELECT $1::uuid,q.section,q.question_text,q.answer_type,q.options,q.correct_option,q.display_order,$3::text,$3::text FROM survey_questions q
		JOIN survey_instrument_versions v ON v.id=q.instrument_version_id WHERE v.instrument_id=$2 AND v.status='PUBLISHED'`, versionID, instrument.ID, user)
	if err != nil {
		return domain.Draft{}, err
	}
	return s.draftOf(ctx, instrument)
}

func (s *store) ReplaceDraft(ctx context.Context, user string, draft domain.Draft, rowVersion int, questions []domain.Question) (domain.Draft, error) {
	tag, err := s.tx.Exec(ctx, `UPDATE survey_instrument_versions SET row_version=row_version+1,updated_by=$3 WHERE id=$1 AND status='DRAFT' AND row_version=$2`, draft.Version.ID, rowVersion, user)
	if err != nil {
		return domain.Draft{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Draft{}, domain.ErrDraftConflict
	}
	if _, err = s.tx.Exec(ctx, `DELETE FROM survey_questions WHERE instrument_version_id=$1`, draft.Version.ID); err != nil {
		return domain.Draft{}, err
	}
	if err = s.insertQuestions(ctx, user, draft.Version.ID, questions); err != nil {
		return domain.Draft{}, err
	}
	return s.draftOf(ctx, draft.Instrument)
}

func (s *store) PublishDraft(ctx context.Context, user string, draft domain.Draft, at time.Time) (domain.Version, error) {
	// La versión vigente se archiva primero para respetar la unicidad de una sola PUBLISHED.
	if _, err := s.tx.Exec(ctx, `UPDATE survey_instrument_versions SET status='ARCHIVED',updated_by=$2 WHERE instrument_id=$1 AND status='PUBLISHED'`, draft.Instrument.ID, user); err != nil {
		return domain.Version{}, err
	}
	v, err := scanVersion(s.tx.QueryRow(ctx, `UPDATE survey_instrument_versions v SET status='PUBLISHED',published_at=$2,updated_by=$3 WHERE v.id=$1 AND v.status='DRAFT' RETURNING `+versionColumns, draft.Version.ID, at, user))
	if err != nil {
		return v, err
	}
	_, err = s.tx.Exec(ctx, `UPDATE survey_instruments SET status='PUBLISHED',updated_by=$2 WHERE id=$1`, draft.Instrument.ID, user)
	return v, err
}

// surveyVersion lee una versión no borrador del instrumento CAP con sus preguntas.
func (s *store) surveyVersion(ctx context.Context, where string, args ...any) (*domain.SurveyVersion, error) {
	var sv domain.SurveyVersion
	i, v := &sv.Instrument, &sv.Version
	err := s.tx.QueryRow(ctx, `SELECT `+instrumentColumns+`,`+versionColumns+` FROM survey_instrument_versions v JOIN survey_instruments i ON i.id=v.instrument_id
		WHERE i.is_cap AND `+where, args...).Scan(&i.ID, &i.Code, &i.Name, &i.Description, &i.Status, &i.IsCAP, &i.CreatedAt,
		&v.ID, &v.InstrumentID, &v.Status, &v.Number, &v.RowVersion, &v.PublishedAt, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sv.Questions, err = s.questions(ctx, v.ID); err != nil {
		return nil, err
	}
	return &sv, nil
}

func (s *store) PublishedCAP(ctx context.Context) (*domain.SurveyVersion, error) {
	return s.surveyVersion(ctx, `v.status='PUBLISHED'`)
}

func (s *store) CAPVersion(ctx context.Context, versionID string) (*domain.SurveyVersion, error) {
	sv, err := s.surveyVersion(ctx, `v.id=$1 AND v.status IN ('PUBLISHED','ARCHIVED')`, versionID)
	if err == nil && sv == nil {
		err = domain.ErrVersionNotFound
	}
	return sv, err
}
