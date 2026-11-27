package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/jackc/pgx/v5"
)

// ReportedAlertType es el aviso que reciben el jefe de cocina y el administrador cuando alguien reporta un problema.
const ReportedAlertType = "INCIDENT_REPORTED"

func reportedAlertsSupported(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT to_regprocedure('alert_incident_reported_supported()') IS NOT NULL`).Scan(&ok)
	return ok, e
}

// NotifyReported crea (push solo si es urgente), en la misma transacción del reporte, un aviso para cada ADMIN y cada CHEF activo de la sede
// (el ADMIN ve todas; el CHEF solo si tiene esa sede), sin incluir a quien reportó. Se ejecuta en un savepoint y nunca
// rompe el reporte: sin V23 o ante un fallo se registra y se omite. El texto no incluye datos de personas.
func (s *store) NotifyReported(ctx context.Context, actor domain.Actor, org string, v domain.Incident, urgent bool, now time.Time) error {
	ok, e := reportedAlertsSupported(ctx, s.tx)
	if e != nil {
		s.skip("probe-reported", e)
		return nil
	}
	if !ok {
		return nil
	}
	sp, e := s.tx.Begin(ctx)
	if e != nil {
		s.skip("savepoint-reported", e)
		return nil
	}
	if e = s.emitReported(ctx, sp, actor, org, v, urgent, now); e != nil {
		sp.Rollback(context.Background())
		s.skip("emit-reported", e)
		return nil
	}
	if e = sp.Commit(ctx); e != nil {
		s.skip("release-reported", e)
	}
	return nil
}

func (s *store) emitReported(ctx context.Context, tx pgx.Tx, actor domain.Actor, org string, v domain.Incident, urgent bool, now time.Time) error {
	var site string
	if e := tx.QueryRow(ctx, `SELECT name FROM locations WHERE organization_id=$1 AND id=$2`, org, v.LocationID).Scan(&site); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, `SELECT m.user_id FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 WHERE m.organization_id=$1 AND m.status='ACTIVE' AND m.user_id<>$3 AND (m.role='ADMIN' OR (m.role='CHEF' AND EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=m.organization_id AND ml.user_id=m.user_id AND ml.location_id=$2)))
 ORDER BY m.user_id LIMIT 50`, org, v.LocationID, actor.ID)
	if e != nil {
		return e
	}
	var recipients []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		recipients = append(recipients, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	prefix, priority := "Nuevo problema reportado: ", domain.PriorityNormal
	if urgent {
		prefix, priority = "Problema urgente: ", domain.PriorityUrgent
	}
	title := truncateRunes(prefix+v.Name, 160)
	message := truncateRunes(site+" · Revísalo y asigna quién lo corrige.", 500)
	snapshot, e := json.Marshal(map[string]any{"emailEnabled": false, "pushEnabled": urgent, "priority": priority, "configuration": map[string]any{}})
	if e != nil {
		return e
	}
	for _, user := range recipients {
		var id string
		e = tx.QueryRow(ctx, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,dedup_key,context_snapshot,created_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,'INCIDENT',$7,$8,$9,$10,$11,$11) ON CONFLICT(organization_id,dedup_key) DO NOTHING RETURNING id::text`,
			org, v.LocationID, user, ReportedAlertType, title, message, v.ID, "incident-reported:"+v.ID+":"+user, snapshot, now, s.actor.ID).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		if !urgent {
			// Normal: solo bandeja; el push (y su evento de salida) es para lo urgente.
			continue
		}
		if e = outbox.Enqueue(ctx, tx, outbox.Event{OrganizationID: org, Type: "notifications.alert.ready.v1", AggregateType: "alert", AggregateID: id, DedupKey: "alert:" + id, Payload: json.RawMessage(`{}`), CreatedAt: now, ExpiresAt: now.Add(23 * time.Hour)}); e != nil {
			return e
		}
	}
	return nil
}
