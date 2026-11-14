package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"strconv"

	"github.com/Nidka/BPM-service/internal/features/checklists/application"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }
type store struct{ tx pgx.Tx }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Within(ctx context.Context, fn func(application.Store) error) error {
	return r.within(ctx, func(s *store) error { return fn(s) })
}

func (r *Repository) within(ctx context.Context, fn func(*store) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = fn(&store{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *store) Actor(ctx context.Context, uid, org string) (a domain.Actor, err error) {
	err = s.tx.QueryRow(ctx, `SELECT u.id,u.platform_role FROM application_users u WHERE u.id=$1 AND u.status='ACTIVE' FOR SHARE`, uid).Scan(&a.ID, &a.PlatformRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	if err != nil {
		return a, err
	}
	var active string
	err = s.tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 AND status='ACTIVE' FOR SHARE`, org).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	if err != nil {
		return a, err
	}
	if a.PlatformRole == "SUPER_ADMIN" {
		a.Role = "SUPER_ADMIN"
		return a, nil
	}
	err = s.tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' FOR SHARE`, org, uid).Scan(&a.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrDenied
	}
	return a, err
}

func (s *store) LocationAccess(ctx context.Context, a domain.Actor, org, locationID string) error {
	var found string
	// Incluso SUPER_ADMIN necesita una membresía activa para ejecutar controles:
	// checklist_runs.performed_by referencia una membresía del tenant.
	err := s.tx.QueryRow(ctx, `SELECT l.id::text FROM locations l
		JOIN organization_memberships m ON m.organization_id=l.organization_id AND m.user_id=$2 AND m.status='ACTIVE'
		WHERE l.organization_id=$1 AND l.id=$3 AND l.status='ACTIVE' AND
		(m.role='ADMIN' OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=l.organization_id AND ml.user_id=$2 AND ml.location_id=l.id))
		FOR SHARE OF l,m`, org, a.ID, locationID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrDenied
	}
	return err
}

func pgConflict(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && (p.Code == "23505" || p.Code == "23503") {
		return domain.ErrConflict
	}
	return err
}

func (s *store) audit(ctx context.Context, a domain.Actor, org, action, kind, id string) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$2,$2)`, org, a.ID, action, kind, id)
	return err
}

func parseNullable(value *string) (*float64, error) {
	if value == nil {
		return nil, nil
	}
	v, err := strconv.ParseFloat(*value, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid stored numeric value: %w", err)
	}
	return &v, nil
}

func (s *store) Catalog(ctx context.Context, org string) ([]domain.Template, error) {
	items, e := s.CatalogPage(ctx, org, pagination.Window{Size: 100}, "")
	if len(items) > 100 {
		items = items[:100]
	}
	return items, e
}
func (s *store) CatalogPage(ctx context.Context, org string, w pagination.Window, id string) ([]domain.Template, error) {
	rows, err := s.tx.Query(ctx, `WITH page AS (
 SELECT t.id,t.organization_id,t.name,t.description,t.area,v.id AS version_id,v.version_number FROM checklist_templates t
 JOIN LATERAL (SELECT id,version_number FROM checklist_template_versions WHERE organization_id=t.organization_id AND template_id=t.id AND status='PUBLISHED' ORDER BY version_number DESC LIMIT 1) v ON true
 WHERE t.organization_id=$1 AND t.status='ACTIVE' AND ($6='' OR t.id::text=$6)
 AND ($2='' OR strpos(lower(t.name||' '||t.area||' '||COALESCE(t.description,'')),lower($2))>0)
 AND ($4='' OR (t.name,t.id::text)>($3,$4)) ORDER BY t.name,t.id::text LIMIT $5
 ) SELECT t.id::text,t.name,COALESCE(t.description,''),t.area,v.id::text,v.version_number,
		i.id::text,i.section_name,i.label,i.response_type,i.display_order,i.required,
		i.minimum_value::text,i.maximum_value::text,COALESCE(i.unit,''),i.evidence_required_on_failure
 FROM page t
 JOIN checklist_template_versions v ON v.organization_id=t.organization_id AND v.id=t.version_id
 JOIN checklist_items i ON i.organization_id=t.organization_id AND i.template_version_id=v.id
 ORDER BY t.name,t.id::text,i.display_order,i.id`, org, w.Query, w.After.Key, w.After.ID, w.Size+1, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Template{}
	positions := map[string]int{}
	for rows.Next() {
		var t domain.Template
		var i domain.Item
		var min, max *string
		if err = rows.Scan(&t.ID, &t.Name, &t.Description, &t.Area, &t.VersionID, &t.VersionNumber,
			&i.ID, &i.SectionName, &i.Label, &i.ResponseType, &i.DisplayOrder, &i.Required, &min, &max, &i.Unit, &i.EvidenceRequiredOnFailure); err != nil {
			return nil, err
		}
		if i.MinimumValue, err = parseNullable(min); err != nil {
			return nil, err
		}
		if i.MaximumValue, err = parseNullable(max); err != nil {
			return nil, err
		}
		at, ok := positions[t.ID]
		if !ok {
			t.Items = []domain.Item{}
			at = len(out)
			positions[t.ID] = at
			out = append(out, t)
		}
		out[at].Items = append(out[at].Items, i)
	}
	return out, rows.Err()
}

func (s *store) CreateTemplate(ctx context.Context, a domain.Actor, org string, t domain.Template) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO checklist_templates(id,organization_id,name,description,area,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,$6)`, t.ID, org, t.Name, t.Description, t.Area, a.ID)
	if err != nil {
		return pgConflict(err)
	}
	_, err = s.tx.Exec(ctx, `INSERT INTO checklist_template_versions(id,organization_id,template_id,version_number,status,published_at,created_by,updated_by)
		VALUES($1,$2,$3,1,'PUBLISHED',NOW(),$4,$4)`, t.VersionID, org, t.ID, a.ID)
	if err != nil {
		return pgConflict(err)
	}
	for _, i := range t.Items {
		_, err = s.tx.Exec(ctx, `INSERT INTO checklist_items(id,organization_id,template_version_id,section_name,label,response_type,display_order,required,minimum_value,maximum_value,unit,evidence_required_on_failure,created_by,updated_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$13)`, i.ID, org, t.VersionID, i.SectionName, i.Label, i.ResponseType, i.DisplayOrder, i.Required, i.MinimumValue, i.MaximumValue, i.Unit, i.EvidenceRequiredOnFailure, a.ID)
		if err != nil {
			return pgConflict(err)
		}
	}
	return s.audit(ctx, a, org, "checklist.template.published", "checklist_template", t.ID)
}

func (s *store) CreateRun(ctx context.Context, a domain.Actor, v domain.Run) (domain.Run, error) {
	tag, err := s.tx.Exec(ctx, `INSERT INTO checklist_runs(id,organization_id,location_id,template_version_id,performed_by,client_operation_id,client_created_at,created_by,updated_by,assignment_id,created_at)
		SELECT $1::uuid,$2::varchar(64),$3::uuid,v.id,$4::varchar(128),$5::uuid,$6::timestamptz,$4::varchar(128),$4::varchar(128),NULLIF($8,'')::uuid,$9::timestamptz FROM checklist_template_versions v
		JOIN checklist_templates t ON t.organization_id=v.organization_id AND t.id=v.template_id
		WHERE v.organization_id=$2::varchar(64) AND v.id=$7::uuid AND v.status='PUBLISHED' AND t.status='ACTIVE'
		ON CONFLICT(id) DO NOTHING`, v.ID, v.OrganizationID, v.LocationID, a.ID, v.ClientOperationID, v.ClientCreatedAt, v.TemplateVersionID, v.AssignmentID, v.CreatedAt)
	if err != nil {
		return domain.Run{}, pgConflict(err)
	}
	if tag.RowsAffected() == 0 {
		var same bool
		err = s.tx.QueryRow(ctx, `SELECT organization_id=$2 AND location_id=$3 AND template_version_id=$4 AND performed_by=$5 AND client_operation_id=$6 AND client_created_at=$7 AND COALESCE(assignment_id::text,'')=$8
			FROM checklist_runs WHERE id=$1`, v.ID, v.OrganizationID, v.LocationID, v.TemplateVersionID, a.ID, v.ClientOperationID, v.ClientCreatedAt, v.AssignmentID).Scan(&same)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Run{}, domain.ErrNotFound
		}
		if err != nil {
			return domain.Run{}, err
		}
		if !same {
			return domain.Run{}, domain.ErrConflict
		}
	} else if err = s.audit(ctx, a, v.OrganizationID, "checklist.run.started", "checklist_run", v.ID); err != nil {
		return domain.Run{}, err
	}
	return s.Run(ctx, a, v.OrganizationID, v.ID, false)
}

func (s *store) Run(ctx context.Context, a domain.Actor, org, runID string, lock bool) (v domain.Run, err error) {
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE"
	}
	var score *string
	err = s.tx.QueryRow(ctx, `SELECT id::text,organization_id,location_id::text,template_version_id::text,performed_by,
		client_operation_id::text,client_created_at,status,row_version,compliance_percent::text,submitted_at,created_at,COALESCE(assignment_id::text,'')
		FROM checklist_runs WHERE organization_id=$1 AND id=$2`+lockClause, org, runID).Scan(
		&v.ID, &v.OrganizationID, &v.LocationID, &v.TemplateVersionID, &v.PerformedBy,
		&v.ClientOperationID, &v.ClientCreatedAt, &v.Status, &v.RowVersion, &score, &v.SubmittedAt, &v.CreatedAt, &v.AssignmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if v.CompliancePercent, err = parseNullable(score); err != nil {
		return v, err
	}
	if a.Role != "SUPER_ADMIN" {
		if err = s.LocationAccess(ctx, a, org, v.LocationID); err != nil {
			return v, err
		}
	}
	if a.ID != v.PerformedBy && a.Role != "ADMIN" && a.Role != "CHEF" && a.Role != "SUPER_ADMIN" {
		return v, domain.ErrDenied
	}
	if err = s.tx.QueryRow(ctx, `SELECT t.name,l.name,u.display_name FROM checklist_template_versions v
 JOIN checklist_templates t ON t.organization_id=v.organization_id AND t.id=v.template_id
 JOIN locations l ON l.organization_id=v.organization_id AND l.id=$3
 JOIN application_users u ON u.id=$4
 WHERE v.organization_id=$1 AND v.id=$2`, org, v.TemplateVersionID, v.LocationID, v.PerformedBy).Scan(&v.TemplateName, &v.LocationName, &v.PerformedByName); err != nil {
		return v, err
	}
	rows, err := s.tx.Query(ctx, `SELECT id::text,section_name,label,response_type,display_order,required,minimum_value::text,maximum_value::text,COALESCE(unit,''),evidence_required_on_failure
		FROM checklist_items WHERE organization_id=$1 AND template_version_id=$2 ORDER BY display_order`, org, v.TemplateVersionID)
	if err != nil {
		return v, err
	}
	v.Items = []domain.Item{}
	for rows.Next() {
		var i domain.Item
		var min, max *string
		if err = rows.Scan(&i.ID, &i.SectionName, &i.Label, &i.ResponseType, &i.DisplayOrder, &i.Required, &min, &max, &i.Unit, &i.EvidenceRequiredOnFailure); err != nil {
			break
		}
		if i.MinimumValue, err = parseNullable(min); err != nil {
			break
		}
		if i.MaximumValue, err = parseNullable(max); err != nil {
			break
		}
		v.Items = append(v.Items, i)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return v, err
	}
	rows, err = s.tx.Query(ctx, `SELECT checklist_item_id::text,COALESCE(compliance_result,''),numeric_value::text,COALESCE(text_value,''),COALESCE(observation,'')
		FROM checklist_answers WHERE organization_id=$1 AND checklist_run_id=$2 ORDER BY checklist_item_id`, org, runID)
	if err != nil {
		return v, err
	}
	v.Answers = []domain.Answer{}
	for rows.Next() {
		var answer domain.Answer
		var numeric *string
		if err = rows.Scan(&answer.ItemID, &answer.ComplianceResult, &numeric, &answer.TextValue, &answer.Observation); err != nil {
			break
		}
		if answer.NumericValue, err = parseNullable(numeric); err != nil {
			break
		}
		v.Answers = append(v.Answers, answer)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return v, err
	}
	v.EvidenceCounts = map[string]int{}
	evidence, err := s.tx.Query(ctx, `SELECT cm.checklist_item_id::text,count(*) FROM checklist_media cm
        JOIN media_files m ON m.organization_id=cm.organization_id AND m.id=cm.media_file_id AND m.status='AVAILABLE' AND m.location_id=$4
        JOIN checklist_items i ON i.organization_id=cm.organization_id AND i.id=cm.checklist_item_id AND i.template_version_id=$3
        WHERE cm.organization_id=$1 AND cm.checklist_run_id=$2 GROUP BY cm.checklist_item_id`, org, runID, v.TemplateVersionID, v.LocationID)
	if err != nil {
		return v, err
	}
	defer evidence.Close()
	for evidence.Next() {
		var id string
		var count int
		if err = evidence.Scan(&id, &count); err != nil {
			return v, err
		}
		v.EvidenceCounts[id] = count
	}
	return v, evidence.Err()
}

func (s *store) Replay(ctx context.Context, org, key, hash string) (domain.Run, bool, error) {
	var storedHash string
	var body []byte
	err := s.tx.QueryRow(ctx, `SELECT request_hash,response_body FROM idempotency_records WHERE organization_id=$1 AND idempotency_key=$2`, org, key).Scan(&storedHash, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, err
	}
	if storedHash != hash || len(body) == 0 {
		return domain.Run{}, false, domain.ErrConflict
	}
	var run domain.Run
	if err = json.Unmarshal(body, &run); err != nil {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (s *store) saveReceipt(ctx context.Context, actor domain.Actor, org, key, hash string, run domain.Run) error {
	body, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = s.tx.Exec(ctx, `INSERT INTO idempotency_records(organization_id,idempotency_key,request_hash,response_status,response_body,expires_at,created_by,updated_by)
		VALUES($1,$2,$3,200,$4,NOW()+INTERVAL '24 hours',$5,$5)`, org, key, hash, body, actor.ID)
	return pgConflict(err)
}

func (s *store) SaveAnswers(ctx context.Context, a domain.Actor, v domain.Run, answers []domain.Answer, key, hash string) (domain.Run, error) {
	ids := make([]string, 0, len(answers))
	for _, answer := range answers {
		ids = append(ids, answer.ItemID)
	}
	_, err := s.tx.Exec(ctx, `DELETE FROM checklist_answers WHERE organization_id=$1 AND checklist_run_id=$2 AND NOT (checklist_item_id=ANY($3::uuid[]))`, v.OrganizationID, v.ID, ids)
	if err != nil {
		return v, err
	}
	for _, answer := range answers {
		_, err = s.tx.Exec(ctx, `INSERT INTO checklist_answers(organization_id,checklist_run_id,checklist_item_id,compliance_result,numeric_value,text_value,observation,created_by,updated_by)
			VALUES($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),NULLIF($7,''),$8,$8)
            ON CONFLICT(checklist_run_id,checklist_item_id) DO UPDATE SET compliance_result=EXCLUDED.compliance_result,numeric_value=EXCLUDED.numeric_value,text_value=EXCLUDED.text_value,observation=EXCLUDED.observation,updated_by=EXCLUDED.updated_by`, v.OrganizationID, v.ID, answer.ItemID,
			answer.ComplianceResult, answer.NumericValue, answer.TextValue, answer.Observation, a.ID)
		if err != nil {
			return v, pgConflict(err)
		}
	}
	_, err = s.tx.Exec(ctx, `UPDATE checklist_runs SET row_version=row_version+1,updated_by=$3 WHERE organization_id=$1 AND id=$2`, v.OrganizationID, v.ID, a.ID)
	if err != nil {
		return v, err
	}
	if err = s.audit(ctx, a, v.OrganizationID, "checklist.answers.saved", "checklist_run", v.ID); err != nil {
		return v, err
	}
	v, err = s.Run(ctx, a, v.OrganizationID, v.ID, false)
	if err != nil {
		return v, err
	}
	return v, s.saveReceipt(ctx, a, v.OrganizationID, key, hash, v)
}

func (s *store) Submit(ctx context.Context, a domain.Actor, v domain.Run, score float64, key, hash string) (domain.Run, error) {
	_, err := s.tx.Exec(ctx, `UPDATE checklist_runs SET status='SUBMITTED',compliance_percent=$3,submitted_at=NOW(),row_version=row_version+1,updated_by=$4
		WHERE organization_id=$1 AND id=$2`, v.OrganizationID, v.ID, score, a.ID)
	if err != nil {
		return v, err
	}
	if err = s.audit(ctx, a, v.OrganizationID, "checklist.run.submitted", "checklist_run", v.ID); err != nil {
		return v, err
	}
	v, err = s.Run(ctx, a, v.OrganizationID, v.ID, false)
	if err != nil {
		return v, err
	}
	return v, s.saveReceipt(ctx, a, v.OrganizationID, key, hash, v)
}

var _ application.UnitOfWork = (*Repository)(nil)
var _ application.Store = (*store)(nil)
