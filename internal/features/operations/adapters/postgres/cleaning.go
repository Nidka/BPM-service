package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"reflect"
)

const planFields = `id::text,location_id::text,area,task,COALESCE(instructions,''),COALESCE(product_name,''),frequency,COALESCE(left(due_time::text,5),''),active`

func scanPlan(row pgx.Row) (v domain.CleaningPlan, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.Area, &v.Task, &v.Instructions, &v.ProductName, &v.Frequency, &v.DueTime, &v.Active)
	return v, missing(e)
}
func (s *store) Plan(ctx context.Context, org, id string) (domain.CleaningPlan, error) {
	return scanPlan(s.tx.QueryRow(ctx, `SELECT `+planFields+` FROM cleaning_plans WHERE organization_id=$1 AND id=$2 FOR SHARE`, org, id))
}
func (s *store) CreatePlan(ctx context.Context, a domain.Actor, org string, v domain.CleaningPlan) (domain.CleaningPlan, error) {
	tag, e := s.tx.Exec(ctx, `INSERT INTO cleaning_plans(id,organization_id,location_id,area,task,instructions,product_name,frequency,due_time,active,created_by,updated_by) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,NULLIF($9,'')::time,true,$10,$10) ON CONFLICT DO NOTHING`, v.ID, org, v.LocationID, v.Area, v.Task, v.Instructions, v.ProductName, v.Frequency, v.DueTime, a.ID)
	if e != nil {
		return v, conflict(e)
	}
	saved, e := s.Plan(ctx, org, v.ID)
	if e != nil {
		if e == domain.ErrNotFound {
			return v, domain.ErrConflict
		}
		return v, e
	}
	if tag.RowsAffected() == 0 {
		var creator string
		e = s.tx.QueryRow(ctx, `SELECT COALESCE(created_by,'') FROM cleaning_plans WHERE organization_id=$1 AND id=$2`, org, v.ID).Scan(&creator)
		if e != nil {
			return v, e
		}
		if creator != a.ID || !reflect.DeepEqual(saved, v) {
			return v, domain.ErrConflict
		}
		return saved, nil
	}
	return saved, s.Audit(ctx, a, org, "cleaning_plan.created", v.ID)
}
func (s *store) Plans(ctx context.Context, org, site string, w pagination.Window) ([]domain.CleaningPlan, error) {
	if e := catalogWindow(w); e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+planFields+` FROM cleaning_plans WHERE organization_id=$1 AND location_id=$2 AND strpos(lower(area || ' ' || task || ' ' || COALESCE(product_name,'')),lower($3))>0 AND ($5='' OR (lower(area || ' ' || task) COLLATE "C",id)>($4 COLLATE "C",NULLIF($5,'')::uuid)) ORDER BY lower(area || ' ' || task) COLLATE "C",id LIMIT $6`, org, site, w.Query, w.After.Key, w.After.ID, limit(w))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.CleaningPlan{}
	for rows.Next() {
		v, e := scanPlan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const executionFields = `e.id::text,e.location_id::text,e.cleaning_plan_id::text,p.area,p.task,e.responsible_user_id,u.display_name,e.executed_at,e.result,COALESCE(e.observation,''),e.client_operation_id::text,e.client_created_at,e.created_at,COALESCE(e.request_hash,'')`
const executionJoins = ` FROM cleaning_executions e JOIN cleaning_plans p ON p.organization_id=e.organization_id AND p.id=e.cleaning_plan_id JOIN application_users u ON u.id=e.responsible_user_id `

func scanExecution(row pgx.Row) (v domain.CleaningExecution, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.CleaningPlanID, &v.Area, &v.Task, &v.ResponsibleUserID, &v.ResponsibleName, &v.ExecutedAt, &v.Result, &v.Observation, &v.ClientOperationID, &v.ClientCreatedAt, &v.CreatedAt, &v.RequestHash)
	v.ExecutedAt = v.ExecutedAt.UTC()
	v.ClientCreatedAt = v.ClientCreatedAt.UTC()
	v.CreatedAt = v.CreatedAt.UTC()
	return v, missing(e)
}
func (s *store) Execution(ctx context.Context, org, id string) (domain.CleaningExecution, error) {
	return scanExecution(s.tx.QueryRow(ctx, `SELECT `+executionFields+executionJoins+`WHERE e.organization_id=$1 AND e.id=$2`, org, id))
}
func (s *store) CreateExecution(ctx context.Context, a domain.Actor, org string, v domain.CleaningExecution) (domain.CleaningExecution, error) {
	if !hashPattern.MatchString(v.RequestHash) {
		return v, domain.ErrConflict
	}
	tag, e := s.tx.Exec(ctx, `INSERT INTO cleaning_executions(id,organization_id,location_id,cleaning_plan_id,responsible_user_id,executed_at,result,observation,client_operation_id,client_created_at,request_hash,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,$5,$5) ON CONFLICT DO NOTHING`, v.ID, org, v.LocationID, v.CleaningPlanID, a.ID, v.ExecutedAt, v.Result, v.Observation, v.ClientOperationID, v.ClientCreatedAt, v.RequestHash)
	if e != nil {
		return v, conflict(e)
	}
	if tag.RowsAffected() == 0 {
		var id, hash, user, operation string
		e = s.tx.QueryRow(ctx, `SELECT id::text,COALESCE(request_hash,''),responsible_user_id,client_operation_id::text FROM cleaning_executions WHERE organization_id=$1 AND (id=$2 OR client_operation_id=$3) ORDER BY id LIMIT 1 FOR SHARE`, org, v.ID, v.ClientOperationID).Scan(&id, &hash, &user, &operation)
		if errors.Is(e, pgx.ErrNoRows) {
			return v, domain.ErrConflict
		}
		if e != nil {
			return v, e
		}
		if id != v.ID || hash != v.RequestHash || user != a.ID || operation != v.ClientOperationID {
			return v, domain.ErrConflict
		}
		return s.Execution(ctx, org, id)
	}
	if e = s.Audit(ctx, a, org, "cleaning_execution.created", v.ID); e != nil {
		return v, e
	}
	return s.Execution(ctx, org, v.ID)
}
func (s *store) Executions(ctx context.Context, a domain.Actor, org, site string, w pagination.Window) ([]domain.CleaningExecution, error) {
	after, e := dateWindow(w)
	if e != nil {
		return nil, e
	}
	rows, e := s.tx.Query(ctx, `SELECT `+executionFields+executionJoins+` WHERE e.organization_id=$1 AND e.location_id=$2 AND ($7<>'WORKER' OR e.responsible_user_id=$8) AND strpos(lower(p.area || ' ' || p.task || ' ' || u.display_name || ' ' || COALESCE(e.observation,'')),lower($3))>0 AND ($5='' OR (e.executed_at,e.id)<($4,NULLIF($5,'')::uuid)) ORDER BY e.executed_at DESC,e.id DESC LIMIT $6`, org, site, w.Query, after, w.After.ID, limit(w), a.Role, a.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.CleaningExecution{}
	for rows.Next() {
		v, e := scanExecution(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
