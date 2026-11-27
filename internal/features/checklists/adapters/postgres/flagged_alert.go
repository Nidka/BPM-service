package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/jackc/pgx/v5"
)

// FlaggedAlertType es el aviso al jefe de cocina y al administrador por un control con «No cumple» u observación.
const FlaggedAlertType = "CONTROL_FLAGGED"

// notifyFlagged crea, en la misma transacción del envío, el aviso (bandeja + push) para cada ADMIN y cada CHEF con la sede,
// sin quien hizo el control. Mejor esfuerzo: sin V24 o ante un fallo (aislado en un savepoint) el envío sigue igual.
func (s *store) notifyFlagged(ctx context.Context, a domain.Actor, before, after domain.Run) {
	failures, observations := domain.Flagged(before)
	if failures == 0 && observations == 0 {
		return
	}
	var ok bool
	if s.tx.QueryRow(ctx, `SELECT to_regprocedure('alert_v24_supported()') IS NOT NULL`).Scan(&ok) != nil || !ok {
		return
	}
	sp, e := s.tx.Begin(ctx)
	if e != nil {
		return
	}
	if e = emitFlagged(ctx, sp, a, after, failures, observations); e != nil {
		sp.Rollback(context.Background())
		return
	}
	sp.Commit(ctx)
}

func emitFlagged(ctx context.Context, tx pgx.Tx, a domain.Actor, v domain.Run, failures, observations int) error {
	org := v.OrganizationID
	rows, e := tx.Query(ctx, `SELECT m.user_id FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 WHERE m.organization_id=$1 AND m.status='ACTIVE' AND m.user_id<>$3 AND (m.role='ADMIN' OR (m.role='CHEF' AND EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=m.organization_id AND ml.user_id=m.user_id AND ml.location_id=$2)))
 ORDER BY m.user_id LIMIT 50`, org, v.LocationID, a.ID)
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
	title, message := domain.FlaggedText(v.TemplateName, v.LocationName, failures, observations)
	snapshot, e := json.Marshal(map[string]any{"emailEnabled": false, "pushEnabled": true, "failures": failures, "observations": observations, "configuration": map[string]any{}})
	if e != nil {
		return e
	}
	now := time.Now().UTC()
	for _, user := range recipients {
		var id string
		e = tx.QueryRow(ctx, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,dedup_key,context_snapshot,created_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,'CHECKLIST_RUN',$7,$8,$9,$10,$11,$11) ON CONFLICT(organization_id,dedup_key) DO NOTHING RETURNING id::text`,
			org, v.LocationID, user, FlaggedAlertType, title, message, v.ID, "control-flagged:"+v.ID+":"+user, snapshot, now, a.ID).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		if e = outbox.Enqueue(ctx, tx, outbox.Event{OrganizationID: org, Type: "notifications.alert.ready.v1", AggregateType: "alert", AggregateID: id, DedupKey: "alert:" + id, Payload: json.RawMessage(`{}`), CreatedAt: now, ExpiresAt: now.Add(12 * time.Hour)}); e != nil {
			return e
		}
	}
	return nil
}
