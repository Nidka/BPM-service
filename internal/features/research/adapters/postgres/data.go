package postgres

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

// ---- Entregas del participante ----

func (s *store) FindSubmission(ctx context.Context, org, id, operationID string) ([]application.SubmissionRecord, error) {
	rows, err := s.tx.Query(ctx, `SELECT id::text,client_operation_id::text,participant_id::text,instrument_version_id::text,moment,COALESCE(request_hash,''),
		COALESCE(submitted_at,created_at),client_created_at,COALESCE(knowledge_correct,0),COALESCE(knowledge_total,0),COALESCE(knowledge_score,0)::float8,
		COALESCE(attitude_score,0)::float8,COALESCE(practice_score,0)::float8
		FROM survey_submissions WHERE organization_id=$1 AND (id=$2 OR client_operation_id=$3)`, org, id, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []application.SubmissionRecord{}
	for rows.Next() {
		var r application.SubmissionRecord
		sc := &r.Scores
		if err = rows.Scan(&r.ID, &r.ClientOperationID, &r.ParticipantID, &r.VersionID, &r.Moment, &r.RequestHash, &r.SubmittedAt, &r.ClientCreatedAt,
			&sc.KnowledgeCorrect, &sc.KnowledgeTotal, &sc.Knowledge, &sc.Attitude, &sc.Practice); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) SubmissionExists(ctx context.Context, org, participantID, versionID, moment string) (bool, error) {
	return exists(ctx, s.tx, `SELECT 1 FROM survey_submissions WHERE organization_id=$1 AND participant_id=$2 AND instrument_version_id=$3 AND moment=$4`, org, participantID, versionID, moment)
}

func (s *store) InsertSubmission(ctx context.Context, org, user string, rec application.SubmissionRecord, answers []domain.ScoredAnswer) error {
	sc := rec.Scores
	_, err := s.tx.Exec(ctx, `INSERT INTO survey_submissions(id,organization_id,participant_id,instrument_version_id,moment,submitted_at,client_operation_id,client_created_at,request_hash,
		knowledge_correct,knowledge_total,knowledge_score,attitude_score,practice_score,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)`, rec.ID, org, rec.ParticipantID, rec.VersionID, rec.Moment, rec.SubmittedAt, rec.ClientOperationID,
		rec.ClientCreatedAt, rec.RequestHash, sc.KnowledgeCorrect, sc.KnowledgeTotal, sc.Knowledge, sc.Attitude, sc.Practice, user)
	if _, taken := uniqueViolation(err); taken {
		return domain.ErrDuplicateSubmission
	}
	if err != nil {
		return err
	}
	for _, a := range answers {
		if _, err = s.tx.Exec(ctx, `INSERT INTO survey_answers(organization_id,submission_id,question_id,answer_number,is_correct,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$6)`,
			org, rec.ID, a.QuestionID, a.Value, a.Correct, user); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) Submissions(ctx context.Context, org, participantID string) ([]domain.SubmissionSummary, error) {
	rows, err := s.tx.Query(ctx, `SELECT id::text,moment,instrument_version_id::text,COALESCE(submitted_at,created_at) FROM survey_submissions
		WHERE organization_id=$1 AND participant_id=$2 ORDER BY created_at,id`, org, participantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SubmissionSummary{}
	for rows.Next() {
		var v domain.SubmissionSummary
		if err = rows.Scan(&v.ID, &v.Moment, &v.VersionID, &v.SubmittedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- Resultados consolidados y exportación (solo investigadores) ----

// OrganizationResults agrega por empresa. Excluye a los participantes que
// retiraron su consentimiento. Las diferencias pre/post usan solo a quienes
// respondieron ambos momentos.
func (s *store) OrganizationResults(ctx context.Context, versionID string, w pagination.Window) ([]application.OrganizationResult, error) {
	rows, err := s.tx.Query(ctx, `
WITH s AS MATERIALIZED (
  SELECT x.organization_id,x.participant_id,x.moment,x.knowledge_score,x.attitude_score,x.practice_score
  FROM survey_submissions x JOIN research_participants p ON p.organization_id=x.organization_id AND p.id=x.participant_id
  WHERE x.instrument_version_id=$1 AND p.status<>'WITHDRAWN' AND x.knowledge_score IS NOT NULL
)
SELECT o.id,COALESCE(o.trade_name,o.legal_name),
  (SELECT count(*) FROM research_participants p WHERE p.organization_id=o.id AND p.status<>'WITHDRAWN')::int,
  (count(s.moment) FILTER (WHERE s.moment='PRETEST'))::int,
  round(avg(s.knowledge_score) FILTER (WHERE s.moment='PRETEST'),2)::float8,
  round(avg(s.attitude_score) FILTER (WHERE s.moment='PRETEST'),2)::float8,
  round(avg(s.practice_score) FILTER (WHERE s.moment='PRETEST'),2)::float8,
  (count(s.moment) FILTER (WHERE s.moment='POSTTEST'))::int,
  round(avg(s.knowledge_score) FILTER (WHERE s.moment='POSTTEST'),2)::float8,
  round(avg(s.attitude_score) FILTER (WHERE s.moment='POSTTEST'),2)::float8,
  round(avg(s.practice_score) FILTER (WHERE s.moment='POSTTEST'),2)::float8,
  COALESCE(pd.n,0)::int,pd.dk::float8,pd.da::float8,pd.dp::float8
FROM organizations o
LEFT JOIN s ON s.organization_id=o.id
LEFT JOIN LATERAL (
  SELECT count(*) AS n,
    round(avg(po.knowledge_score-pr.knowledge_score),2) AS dk,
    round(avg(po.attitude_score-pr.attitude_score),2) AS da,
    round(avg(po.practice_score-pr.practice_score),2) AS dp
  FROM s pr JOIN s po ON po.organization_id=pr.organization_id AND po.participant_id=pr.participant_id
  WHERE pr.organization_id=o.id AND pr.moment='PRETEST' AND po.moment='POSTTEST'
) pd ON true
WHERE ($2='' OR o.id>$2)
  AND EXISTS (SELECT 1 FROM research_participants p WHERE p.organization_id=o.id AND p.status<>'WITHDRAWN')
GROUP BY o.id,o.trade_name,o.legal_name,pd.n,pd.dk,pd.da,pd.dp
ORDER BY o.id LIMIT $3`, versionID, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []application.OrganizationResult{}
	for rows.Next() {
		var r application.OrganizationResult
		if err = rows.Scan(&r.OrganizationID, &r.Name, &r.Participants,
			&r.Pretest.N, &r.Pretest.Knowledge, &r.Pretest.Attitude, &r.Pretest.Practice,
			&r.Posttest.N, &r.Posttest.Knowledge, &r.Posttest.Attitude, &r.Posttest.Practice,
			&r.Paired.N, &r.Paired.KnowledgeDelta, &r.Paired.AttitudeDelta, &r.Paired.PracticeDelta); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExportRows devuelve filas anonimizadas: sin email, UID, nombre, ni identificadores
// de usuario o de entrega; solo código seudónimo, empresa, momento, respuestas y puntajes.
func (s *store) ExportRows(ctx context.Context, versionID, org string, limit int) ([]application.ExportRow, error) {
	rows, err := s.tx.Query(ctx, `SELECT p.participant_code,x.organization_id,x.moment,COALESCE(x.submitted_at,x.created_at),
		COALESCE((SELECT array_agg(a.answer_number::int ORDER BY q.display_order) FROM survey_answers a JOIN survey_questions q ON q.id=a.question_id
			WHERE a.organization_id=x.organization_id AND a.submission_id=x.id),'{}'::int[]),
		COALESCE(x.knowledge_correct,0),COALESCE(x.knowledge_total,0),COALESCE(x.knowledge_score,0)::float8,COALESCE(x.attitude_score,0)::float8,COALESCE(x.practice_score,0)::float8
		FROM survey_submissions x JOIN research_participants p ON p.organization_id=x.organization_id AND p.id=x.participant_id
		WHERE x.instrument_version_id=$1 AND p.status<>'WITHDRAWN' AND ($2='' OR x.organization_id=$2)
		ORDER BY x.organization_id,p.participant_code,x.moment LIMIT $3`, versionID, org, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []application.ExportRow{}
	for rows.Next() {
		var r application.ExportRow
		var values []int32
		sc := &r.Scores
		if err = rows.Scan(&r.ParticipantCode, &r.OrganizationID, &r.Moment, &r.SubmittedAt, &values, &sc.KnowledgeCorrect, &sc.KnowledgeTotal, &sc.Knowledge, &sc.Attitude, &sc.Practice); err != nil {
			return nil, err
		}
		r.Answers = make([]int, len(values))
		for i, v := range values {
			r.Answers[i] = int(v)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) Organizations(ctx context.Context, w pagination.Window) ([]application.OrganizationRef, error) {
	rows, err := s.tx.Query(ctx, `SELECT id,COALESCE(trade_name,legal_name) FROM organizations WHERE ($1='' OR id>$1) ORDER BY id LIMIT $2`, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []application.OrganizationRef{}
	for rows.Next() {
		var o application.OrganizationRef
		if err = rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *store) OrganizationExists(ctx context.Context, id string) (bool, error) {
	return exists(ctx, s.tx, `SELECT 1 FROM organizations WHERE id=$1`, id)
}

// ---- Necesidades detectadas (RF-50) ----

const needColumns = `n.id::text,n.organization_id,COALESCE(o.trade_name,o.legal_name),n.category,n.description,n.detected_at,n.investigator_user_id,u.display_name,n.created_at`
const needJoins = ` FROM research_company_needs n JOIN organizations o ON o.id=n.organization_id JOIN application_users u ON u.id=n.investigator_user_id`

func scanNeed(row pgx.Row) (n domain.Need, err error) {
	err = row.Scan(&n.ID, &n.OrganizationID, &n.OrganizationName, &n.Category, &n.Description, &n.DetectedAt, &n.InvestigatorID, &n.InvestigatorName, &n.CreatedAt)
	return
}

func (s *store) AddNeed(ctx context.Context, user string, in domain.NeedInput) (domain.Need, error) {
	var id string
	err := s.tx.QueryRow(ctx, `INSERT INTO research_company_needs(organization_id,category,description,detected_at,investigator_user_id,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$5,$5) RETURNING id::text`, in.OrganizationID, in.Category, in.Description, in.DetectedAt, user).Scan(&id)
	if err != nil {
		return domain.Need{}, err
	}
	return scanNeed(s.tx.QueryRow(ctx, `SELECT `+needColumns+needJoins+` WHERE n.id=$1`, id))
}

func (s *store) Needs(ctx context.Context, org string, w pagination.Window) ([]domain.Need, error) {
	rows, err := s.tx.Query(ctx, `SELECT `+needColumns+needJoins+` WHERE ($1='' OR n.organization_id=$1)
		AND ($2::text='' OR (n.detected_at,n.id)<(NULLIF($2::text,'')::timestamptz,NULLIF($3::text,'')::uuid))
		ORDER BY n.detected_at DESC,n.id DESC LIMIT $4`, org, w.After.Key, w.After.ID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Need{}
	for rows.Next() {
		n, err := scanNeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
