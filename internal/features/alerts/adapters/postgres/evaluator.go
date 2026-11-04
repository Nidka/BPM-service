package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/alerts/domain"
	"github.com/jackc/pgx/v5"
	"time"
)

type candidate struct {
	source, sourceType, user string
	location                 *string
	episode                  string
	snapshot                 map[string]any
}

// EvaluationRules limita la selección a cien reglas, sin mantener bloqueos durante otra regla.
func (r *store) EvaluationRules(ctx context.Context, org, receipt, after string) ([]string, error) {
	rows, e := r.tx.Query(ctx, `SELECT r.id::text FROM alert_rules r JOIN organizations o ON o.id=r.organization_id AND o.status='ACTIVE' WHERE r.active AND ($1='' OR r.organization_id=$1) AND (($2<>'' AND r.alert_type='TEMPERATURE_DANGER') OR ($2='' AND r.alert_type IN ('CHECKLIST_MISSING','ORGANIZATION_INACTIVE','TRAINING_ASSIGNED'))) AND r.id::text>$3 ORDER BY r.id LIMIT 100`, org, receipt, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *store) EvaluateRule(ctx context.Context, id, receipt string, now time.Time) error {
	return r.within(ctx, func(tx pgx.Tx) error {
		var org string
		var v domain.Rule
		e := tx.QueryRow(ctx, `SELECT r.organization_id,r.id::text,r.location_id::text,r.name,r.alert_type,r.configuration,r.active,r.email_enabled,r.push_enabled,r.row_version,r.created_at FROM alert_rules r JOIN organizations o ON o.id=r.organization_id AND o.status='ACTIVE' LEFT JOIN locations l ON l.organization_id=r.organization_id AND l.id=r.location_id WHERE r.id=$1 AND r.active AND (r.location_id IS NULL OR l.status='ACTIVE') FOR SHARE OF r,o`, id).Scan(&org, &v.ID, &v.LocationID, &v.Name, &v.Type, &v.Configuration, &v.Active, &v.EmailEnabled, &v.PushEnabled, &v.RowVersion, &v.CreatedAt)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		if domain.ValidateConfiguration(v.Type, v.Configuration) != nil {
			return nil
		}
		switch v.Type {
		case "TEMPERATURE_DANGER":
			return r.temperature(ctx, tx, org, v, receipt, now)
		case "CHECKLIST_MISSING":
			return r.checklists(ctx, tx, org, v, now)
		case "ORGANIZATION_INACTIVE":
			return r.inactivity(ctx, tx, org, v, now)
		case "TRAINING_ASSIGNED":
			return r.training(ctx, tx, org, v, now)
		}
		return nil
	})
}
func eligible(ctx context.Context, tx pgx.Tx, org string, site *string, roles []string) ([]string, error) {
	rows, e := tx.Query(ctx, `SELECT u.id FROM application_users u JOIN organization_memberships m ON m.user_id=u.id AND m.organization_id=$1 AND m.status='ACTIVE' WHERE u.status='ACTIVE' AND m.role=ANY($2::text[]) AND (m.role='ADMIN' OR $3::uuid IS NULL OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=$1 AND ml.user_id=u.id AND ml.location_id=$3)) ORDER BY u.id FOR SHARE OF u,m`, org, roles, site)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	users := []string{}
	for rows.Next() {
		var u string
		if e = rows.Scan(&u); e != nil {
			return nil, e
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
func emit(ctx context.Context, tx pgx.Tx, org string, v domain.Rule, c candidate, now time.Time) error {
	snapshot := c.snapshot
	if snapshot == nil {
		snapshot = map[string]any{}
	}
	snapshot["configuration"] = v.Configuration
	snapshot["emailEnabled"] = v.EmailEnabled
	snapshot["pushEnabled"] = v.PushEnabled
	raw, _ := json.Marshal(snapshot)
	dedup := fmt.Sprintf("rule:%s:%d:%s:%s:%s", v.ID, v.RowVersion, c.source, c.episode, c.user)
	title, message := content(v.Type)
	var id string
	e := tx.QueryRow(ctx, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,rule_id,rule_version,dedup_key,context_snapshot,created_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(organization_id,dedup_key)DO NOTHING RETURNING id::text`, org, c.location, c.user, v.Type, title, message, c.sourceType, c.source, v.ID, v.RowVersion, dedup, raw, now).Scan(&id)
	if e == pgx.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	return outbox.Enqueue(ctx, tx, outbox.Event{OrganizationID: org, Type: "notifications.alert.ready.v1", AggregateType: "alert", AggregateID: id, DedupKey: "alert:" + id, Payload: json.RawMessage(`{}`), CreatedAt: now, ExpiresAt: now.Add(23 * time.Hour)})
}
func content(kind string) (string, string) {
	switch kind {
	case "TEMPERATURE_DANGER":
		return "Temperatura dentro del intervalo de alerta", "Revisa la medición y el criterio configurado de la recepción."
	case "CHECKLIST_MISSING":
		return "Control diario pendiente", "Revisa el control diario pendiente de la sede."
	case "ORGANIZATION_INACTIVE":
		return "Empresa sin actividad reciente", "Revisa la actividad operativa de la empresa."
	default:
		return "Capacitación asignada", "Revisa la capacitación que tienes asignada."
	}
}
func (r *store) temperature(ctx context.Context, tx pgx.Tx, org string, v domain.Rule, receipt string, now time.Time) error {
	var cfg struct {
		Min float64 `json:"minimumTemperatureCelsius"`
		Max float64 `json:"maximumTemperatureCelsius"`
	}
	json.Unmarshal(v.Configuration, &cfg)
	rows, e := tx.Query(ctx, `SELECT i.id::text,r.location_id::text,i.temperature_celsius::text FROM supply_receipt_items i JOIN supply_receipts r ON r.organization_id=i.organization_id AND r.id=i.receipt_id JOIN locations l ON l.organization_id=r.organization_id AND l.id=r.location_id AND l.status='ACTIVE' WHERE i.organization_id=$1 AND r.id=$2 AND ($3::uuid IS NULL OR r.location_id=$3) AND i.temperature_celsius BETWEEN $4 AND $5 ORDER BY i.id FOR SHARE OF i,r,l`, org, receipt, v.LocationID, cfg.Min, cfg.Max)
	if e != nil {
		return e
	}
	type item struct{ id, site, temp string }
	items := []item{}
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.id, &i.site, &i.temp); e != nil {
			rows.Close()
			return e
		}
		items = append(items, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, i := range items {
		users, e := eligible(ctx, tx, org, &i.site, []string{"ADMIN", "CHEF"})
		if e != nil {
			return e
		}
		for _, u := range users {
			if e = emit(ctx, tx, org, v, candidate{source: i.id, sourceType: "RECEIPT_ITEM", user: u, location: &i.site, snapshot: map[string]any{"temperatureCelsius": i.temp}}, now); e != nil {
				return e
			}
		}
	}
	return nil
}
func (r *store) checklists(ctx context.Context, tx pgx.Tx, org string, v domain.Rule, now time.Time) error {
	var cfg struct {
		After string `json:"afterLocalTime"`
	}
	json.Unmarshal(v.Configuration, &cfg)
	rows, e := tx.Query(ctx, `SELECT a.id::text,a.location_id::text,l.timezone,a.created_at FROM checklist_assignments a JOIN locations l ON l.organization_id=a.organization_id AND l.id=a.location_id AND l.status='ACTIVE' JOIN checklist_templates t ON t.organization_id=a.organization_id AND t.id=a.template_id AND t.status='ACTIVE' WHERE a.organization_id=$1 AND a.active AND a.frequency='DAILY' AND ($2::uuid IS NULL OR a.location_id=$2) AND EXISTS(SELECT 1 FROM checklist_template_versions v WHERE v.organization_id=a.organization_id AND v.template_id=a.template_id AND v.status='PUBLISHED') ORDER BY a.id FOR SHARE OF a,l,t`, org, v.LocationID)
	if e != nil {
		return e
	}
	type assignment struct {
		id, site, zone string
		created        time.Time
	}
	items := []assignment{}
	for rows.Next() {
		var i assignment
		if e = rows.Scan(&i.id, &i.site, &i.zone, &i.created); e != nil {
			rows.Close()
			return e
		}
		items = append(items, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, i := range items {
		z, e := time.LoadLocation(i.zone)
		if e != nil {
			continue
		}
		start, end := domain.LocalDay(now, z)
		local := now.In(z)
		if local.Format("15:04") < cfg.After || !i.created.Before(end) {
			continue
		}
		var done bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM checklist_runs WHERE organization_id=$1 AND assignment_id=$2 AND status='SUBMITTED' AND created_at>=$3 AND created_at<$4)`, org, i.id, start, end).Scan(&done); e != nil {
			return e
		}
		if done {
			continue
		}
		users, e := eligible(ctx, tx, org, &i.site, []string{"CHEF"})
		if e != nil {
			return e
		}
		for _, u := range users {
			if e = emit(ctx, tx, org, v, candidate{source: i.id, sourceType: "CHECKLIST_ASSIGNMENT", user: u, location: &i.site, episode: start.UTC().Format(time.RFC3339), snapshot: map[string]any{"periodStart": start.UTC(), "periodEnd": end.UTC()}}, now); e != nil {
				return e
			}
		}
	}
	return nil
}
func (r *store) inactivity(ctx context.Context, tx pgx.Tx, org string, v domain.Rule, now time.Time) error {
	var cfg struct {
		Days int `json:"inactivityDays"`
	}
	json.Unmarshal(v.Configuration, &cfg)
	var last time.Time
	e := tx.QueryRow(ctx, `SELECT max(t) FROM (SELECT created_at t FROM organizations WHERE id=$1 UNION ALL SELECT created_at FROM supply_receipts WHERE organization_id=$1 UNION ALL SELECT created_at FROM cleaning_executions WHERE organization_id=$1 UNION ALL SELECT created_at FROM incidents WHERE organization_id=$1 UNION ALL SELECT submitted_at FROM checklist_runs WHERE organization_id=$1 AND status='SUBMITTED')activity WHERE t<=$2`, org, now).Scan(&last)
	if e != nil {
		return e
	}
	if now.Sub(last) < time.Duration(cfg.Days)*24*time.Hour {
		return nil
	}
	users, e := eligible(ctx, tx, org, nil, []string{"ADMIN"})
	if e != nil {
		return e
	}
	for _, u := range users {
		if e = emit(ctx, tx, org, v, candidate{source: org, sourceType: "ORGANIZATION", user: u, episode: last.UTC().Format(time.RFC3339Nano), snapshot: map[string]any{"lastActivity": last.UTC()}}, now); e != nil {
			return e
		}
	}
	return nil
}
func (r *store) training(ctx context.Context, tx pgx.Tx, org string, v domain.Rule, now time.Time) error {
	rows, e := tx.Query(ctx, `SELECT x.id::text,x.location_id::text,u.id FROM training_assignments x JOIN training_courses c ON c.organization_id=x.organization_id AND c.id=x.course_id AND c.status='PUBLISHED' JOIN organization_memberships m ON m.organization_id=x.organization_id AND m.status='ACTIVE' AND m.role IN ('CHEF','WORKER') JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE' LEFT JOIN locations l ON l.organization_id=x.organization_id AND l.id=x.location_id WHERE x.organization_id=$1 AND x.status='ACTIVE' AND x.created_at>=$2 AND x.created_at<=$4 AND ($3::uuid IS NULL OR x.location_id=$3) AND (x.location_id IS NULL OR l.status='ACTIVE') AND (x.target_user_id IS NULL OR x.target_user_id=u.id) AND (x.target_role IS NULL OR x.target_role=m.role) AND (x.location_id IS NULL OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=x.organization_id AND ml.user_id=u.id AND ml.location_id=x.location_id)) ORDER BY x.id,u.id FOR SHARE OF x,c,u,m`, org, v.CreatedAt, v.LocationID, now)
	if e != nil {
		return e
	}
	items := []candidate{}
	for rows.Next() {
		var c candidate
		c.sourceType = "TRAINING_ASSIGNMENT"
		if e = rows.Scan(&c.source, &c.location, &c.user); e != nil {
			rows.Close()
			return e
		}
		items = append(items, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, c := range items {
		if e = emit(ctx, tx, org, v, c, now); e != nil {
			return e
		}
	}
	return nil
}
