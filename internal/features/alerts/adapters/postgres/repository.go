// Package postgres persiste alertas con autorización y deduplicación transaccionales.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/features/alerts/application"
	"github.com/Nidka/BPM-service/internal/features/alerts/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Repository struct{ pool *pgxpool.Pool }
type store struct{ tx pgx.Tx }

func New(p *pgxpool.Pool) *Repository { return &Repository{p} }
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, e := r.pool.Query(ctx, `SELECT r.row_version,r.email_enabled,r.push_enabled,a.rule_id,a.rule_version,a.dedup_key,a.context_snapshot,a.row_version,d.target_key,d.target_device_id,d.request_payload,d.first_attempt_at,d.last_attempt_at,d.next_attempt_at,d.lease_token,d.locked_until,o.dedup_key,o.lease_token,o.locked_until,o.expires_at,m.token_hash,alert_recipient_authorized(a.organization_id,a.id,a.recipient_user_id),alert_source_pending(a.organization_id,a.id) FROM alert_rules r CROSS JOIN alerts a CROSS JOIN notification_deliveries d CROSS JOIN outbox_events o CROSS JOIN mobile_devices m LIMIT 0`)
	if e != nil {
		return errors.New("alerts migration V7 is required")
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	var valid bool
	e = r.pool.QueryRow(ctx, `SELECT (SELECT count(*)=3 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND contype='u' AND conname IN ('alerts_dedup_uq','notification_deliveries_target_uq','outbox_events_dedup_uq')) AND (SELECT count(*)=2 FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('mobile_devices_active_token_uq','alerts_personal_page_idx'))`).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return errors.New("alerts migration V7 is required")
	}
	return nil
}
func (r *Repository) Within(ctx context.Context, fn func(application.Store) error) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = fn(&store{tx: tx}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func failure(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(e, &p) && (p.Code == "23505" || p.Code == "23503") {
		return domain.ErrConflict
	}
	return e
}
func actor(ctx context.Context, tx pgx.Tx, user, org string, manager bool) error {
	var role, platform string
	e := tx.QueryRow(ctx, `SELECT COALESCE(m.role,''),u.platform_role FROM application_users u JOIN organizations o ON o.id=$2 AND o.status='ACTIVE' LEFT JOIN organization_memberships m ON m.organization_id=o.id AND m.user_id=u.id AND m.status='ACTIVE' WHERE u.id=$1 AND u.status='ACTIVE' FOR SHARE OF u,o`, user, org).Scan(&role, &platform)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrDenied
	}
	if e != nil {
		return e
	}
	if manager {
		if platform != "SUPER_ADMIN" && role != "ADMIN" {
			return domain.ErrDenied
		}
	} else if role == "" {
		return domain.ErrDenied
	}
	if role != "" {
		var ok string
		e = tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' FOR SHARE`, org, user).Scan(&ok)
		if e != nil {
			return domain.ErrDenied
		}
		if manager && platform != "SUPER_ADMIN" && ok != "ADMIN" {
			return domain.ErrDenied
		}
	}
	return nil
}
func location(ctx context.Context, tx pgx.Tx, org string, id *string) error {
	if id == nil {
		return nil
	}
	var v string
	e := tx.QueryRow(ctx, `SELECT id::text FROM locations WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR SHARE`, org, *id).Scan(&v)
	return failure(e)
}
func audit(ctx context.Context, tx pgx.Tx, u, o, action, id string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,metadata,created_by,updated_by)VALUES($1,$2,$3,'alert',$4,'{}',$2,$2)`, o, u, action, id)
	return e
}

const ruleColumns = `r.id::text,r.location_id::text,COALESCE(l.name,''),r.name,r.alert_type,r.configuration,r.active,r.email_enabled,r.push_enabled,r.row_version,r.created_at`

func scanRule(row pgx.Row) (v domain.Rule, e error) {
	e = row.Scan(&v.ID, &v.LocationID, &v.LocationName, &v.Name, &v.Type, &v.Configuration, &v.Active, &v.EmailEnabled, &v.PushEnabled, &v.RowVersion, &v.CreatedAt)
	return v, failure(e)
}
func (r *store) WriteRule(ctx context.Context, u, o, key string, v domain.Rule, create bool) (saved domain.Rule, e error) {
	e = r.within(ctx, func(tx pgx.Tx) error {
		if e := actor(ctx, tx, u, o, true); e != nil {
			return e
		}
		if e := location(ctx, tx, o, v.LocationID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('alerts-rule:'||$1,0))`, o); e != nil {
			return e
		}
		b, _ := json.Marshal(struct {
			User, ID string
			Create   bool
			Rule     domain.Rule
		}{u, v.ID, create, v})
		hash := fmt.Sprintf("%x", sha256.Sum256(b))
		var old, by string
		var body []byte
		e := tx.QueryRow(ctx, `SELECT request_hash,response_body,COALESCE(created_by,'') FROM idempotency_records WHERE organization_id=$1 AND idempotency_key=$2`, o, key).Scan(&old, &body, &by)
		if e == nil {
			if old != hash || by != u {
				return domain.ErrConflict
			}
			if e = json.Unmarshal(body, &saved); e != nil {
				return e
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if create {
			_, e = tx.Exec(ctx, `INSERT INTO alert_rules(id,organization_id,location_id,name,alert_type,configuration,active,email_enabled,push_enabled,created_by,updated_by)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, v.ID, o, v.LocationID, v.Name, v.Type, v.Configuration, v.Active, v.EmailEnabled, v.PushEnabled, u)
		} else {
			tag, err := tx.Exec(ctx, `UPDATE alert_rules SET name=$4,configuration=$5,active=$6,email_enabled=$7,push_enabled=$8,row_version=row_version+1,updated_by=$9 WHERE organization_id=$1 AND id=$2 AND row_version=$3 AND alert_type=$10 AND location_id IS NOT DISTINCT FROM $11::uuid`, o, v.ID, v.RowVersion, v.Name, v.Configuration, v.Active, v.EmailEnabled, v.PushEnabled, u, v.Type, v.LocationID)
			e = err
			if e == nil && tag.RowsAffected() != 1 {
				return domain.ErrConflict
			}
		}
		if e != nil {
			return failure(e)
		}
		saved, e = scanRule(tx.QueryRow(ctx, `SELECT `+ruleColumns+` FROM alert_rules r LEFT JOIN locations l ON l.organization_id=r.organization_id AND l.id=r.location_id WHERE r.organization_id=$1 AND r.id=$2`, o, v.ID))
		if e != nil {
			return e
		}
		body, _ = json.Marshal(saved)
		_, e = tx.Exec(ctx, `INSERT INTO idempotency_records(organization_id,idempotency_key,request_hash,response_status,response_body,expires_at,created_by,updated_by)VALUES($1,$2,$3,200,$4,NOW()+INTERVAL '24 hours',$5,$5)`, o, key, hash, body, u)
		if e != nil {
			return failure(e)
		}
		action := "alerts.rule.updated"
		if create {
			action = "alerts.rule.created"
		}
		return audit(ctx, tx, u, o, action, v.ID)
	})
	return
}
func (r *store) Rules(ctx context.Context, u, o, l string, p pagination.Request) (page pagination.Page[domain.Rule], e error) {
	scope := []string{u, o, "alert-rules", l}
	w, e := pagination.Open(p, scope...)
	if e != nil {
		return page, e
	}
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return page, pagination.ErrInvalid
	}
	e = r.within(ctx, func(tx pgx.Tx) error {
		if e := actor(ctx, tx, u, o, true); e != nil {
			return e
		}
		if l != "" {
			if e := location(ctx, tx, o, &l); e != nil {
				return e
			}
		}
		rows, e := tx.Query(ctx, `SELECT `+ruleColumns+` FROM alert_rules r LEFT JOIN locations l ON l.organization_id=r.organization_id AND l.id=r.location_id WHERE r.organization_id=$1 AND (r.location_id IS NULL OR l.status='ACTIVE') AND ($2='' OR r.location_id::text=$2) AND strpos(lower(r.name),lower($3))>0 AND ($4='' OR (r.name,r.id::text)>($4,$5)) ORDER BY r.name,r.id LIMIT $6`, o, l, w.Query, w.After.Key, w.After.ID, w.Size+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		items := []domain.Rule{}
		for rows.Next() {
			v, e := scanRule(rows)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		page = pagination.Close(items, w, func(v domain.Rule) pagination.Position { return pagination.Position{Key: v.Name, ID: v.ID} }, scope...)
		return nil
	})
	return
}

const alertColumns = `a.id::text,a.alert_type,a.title,a.message,CASE WHEN a.attended_at IS NOT NULL THEN 'ATTENDED' WHEN a.viewed_at IS NOT NULL THEN 'VIEWED' ELSE 'PENDING' END,a.created_at,a.viewed_at,a.attended_at,a.location_id::text,COALESCE(l.name,''),COALESCE(l.timezone,''),COALESCE(a.source_type,''),COALESCE(a.source_id,''),(SELECT i.receipt_id::text FROM supply_receipt_items i WHERE a.source_type='RECEIPT_ITEM' AND i.organization_id=a.organization_id AND i.id::text=a.source_id),alert_source_pending(a.organization_id,a.id)`

func scanAlert(row pgx.Row) (v domain.Alert, e error) {
	e = row.Scan(&v.ID, &v.Type, &v.Title, &v.Message, &v.Status, &v.CreatedAt, &v.ViewedAt, &v.AttendedAt, &v.LocationID, &v.LocationName, &v.Timezone, &v.SourceType, &v.SourceID, &v.SourceParentID, &v.SourceAvailable)
	return v, failure(e)
}
func detail(ctx context.Context, tx pgx.Tx, u, o, id string) (domain.Alert, error) {
	return scanAlert(tx.QueryRow(ctx, `SELECT `+alertColumns+` FROM alerts a LEFT JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id WHERE a.organization_id=$1 AND a.id=$2 AND alert_recipient_authorized($1,a.id,$3)`, o, id, u))
}
func (r *store) Detail(ctx context.Context, u, o, id string) (v domain.Alert, e error) {
	e = r.within(ctx, func(tx pgx.Tx) error {
		if e := actor(ctx, tx, u, o, false); e != nil {
			return e
		}
		v, e = detail(ctx, tx, u, o, id)
		return e
	})
	return
}
func (r *store) Count(ctx context.Context, u, o string) (n int, e error) {
	e = r.within(ctx, func(tx pgx.Tx) error {
		if e := actor(ctx, tx, u, o, false); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM alerts a WHERE a.organization_id=$1 AND a.recipient_user_id=$2 AND a.viewed_at IS NULL AND a.attended_at IS NULL AND alert_recipient_authorized($1,a.id,$2)`, o, u).Scan(&n)
	})
	return
}
func (r *store) Inbox(ctx context.Context, u, o, state string, p pagination.Request) (page pagination.Page[domain.Alert], e error) {
	scope := []string{u, o, "alerts", state}
	w, e := pagination.Open(p, scope...)
	if e != nil {
		return page, e
	}
	var after *time.Time
	if w.After.ID != "" {
		v, e := time.Parse(time.RFC3339Nano, w.After.Key)
		if e != nil || !domain.ValidID(w.After.ID) {
			return page, pagination.ErrInvalid
		}
		after = &v
	}
	e = r.within(ctx, func(tx pgx.Tx) error {
		if e := actor(ctx, tx, u, o, false); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT `+alertColumns+` FROM alerts a LEFT JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id WHERE a.organization_id=$1 AND a.recipient_user_id=$2 AND alert_recipient_authorized($1,a.id,$2) AND ($3='ALL' OR ($3='UNREAD' AND a.viewed_at IS NULL AND a.attended_at IS NULL) OR ($3='VIEWED' AND a.viewed_at IS NOT NULL AND a.attended_at IS NULL) OR ($3='ATTENDED' AND a.attended_at IS NOT NULL)) AND (strpos(lower(a.title),lower($4))>0 OR strpos(lower(a.message),lower($4))>0) AND ($5::timestamptz IS NULL OR a.created_at<$5 OR (a.created_at=$5 AND a.id::text>$6)) ORDER BY a.created_at DESC,a.id LIMIT $7`, o, u, state, w.Query, after, w.After.ID, w.Size+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		items := []domain.Alert{}
		for rows.Next() {
			v, e := scanAlert(rows)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		page = pagination.Close(items, w, func(v domain.Alert) pagination.Position {
			return pagination.Position{Key: v.CreatedAt.Format(time.RFC3339Nano), ID: v.ID}
		}, scope...)
		return nil
	})
	return
}
func (r *store) Transition(ctx context.Context, u, o, id string, attend bool) (v domain.Alert, e error) {
	e = r.within(ctx, func(tx pgx.Tx) error {
		if e := actor(ctx, tx, u, o, false); e != nil {
			return e
		}
		var locked string
		if e := tx.QueryRow(ctx, `SELECT id::text FROM alerts WHERE organization_id=$1 AND id=$2 AND recipient_user_id=$3 FOR UPDATE`, o, id, u).Scan(&locked); e != nil {
			return failure(e)
		}
		before, e := detail(ctx, tx, u, o, id)
		if e != nil {
			return e
		}
		changed := before.ViewedAt == nil || (attend && before.AttendedAt == nil)
		if changed {
			_, e = tx.Exec(ctx, `UPDATE alerts SET viewed_at=COALESCE(viewed_at,NOW()),attended_at=CASE WHEN $3 THEN COALESCE(attended_at,NOW()) ELSE attended_at END,status=CASE WHEN $3 OR attended_at IS NOT NULL THEN 'ATTENDED' ELSE 'VIEWED' END,row_version=row_version+1,updated_by=$4 WHERE organization_id=$1 AND id=$2`, o, id, attend, u)
			if e != nil {
				return e
			}
			action := "alerts.view"
			if attend {
				action = "alerts.attend"
			}
			if e = audit(ctx, tx, u, o, action, id); e != nil {
				return e
			}
		}
		v, e = detail(ctx, tx, u, o, id)
		return e
	})
	return
}

func (r *store) within(ctx context.Context, fn func(pgx.Tx) error) error { return fn(r.tx) }
