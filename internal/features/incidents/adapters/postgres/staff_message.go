package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/jackc/pgx/v5"
)

// StaffMessageAlertType es el aviso de «Avisar a mi jefe».
const StaffMessageAlertType = "STAFF_MESSAGE"

func v24Supported(ctx context.Context, tx pgx.Tx) bool {
	var ok bool
	return tx.QueryRow(ctx, `SELECT to_regprocedure('alert_v24_supported()') IS NOT NULL`).Scan(&ok) == nil && ok
}

// InsertStaffMessage guarda el aviso rápido (registro mínimo para auditoría). Sin V24 responde ErrUnavailable.
func (s *store) InsertStaffMessage(ctx context.Context, a domain.Actor, org string, v domain.StaffMessage, now time.Time) (domain.StaffMessage, error) {
	if !v24Supported(ctx, s.tx) {
		return v, domain.ErrUnavailable
	}
	_, e := s.tx.Exec(ctx, `INSERT INTO staff_messages(id,organization_id,location_id,sender_user_id,kind,message,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, v.ID, org, v.LocationID, a.ID, v.Kind, v.Text, now)
	if e != nil {
		return v, conflict(e)
	}
	v.CreatedAt = now
	return v, nil
}

// NotifyStaffMessage avisa (bandeja + push) al jefe de cocina y al administrador de la sede. Mejor esfuerzo en un savepoint.
func (s *store) NotifyStaffMessage(ctx context.Context, a domain.Actor, org string, v domain.StaffMessage, now time.Time) error {
	sp, e := s.tx.Begin(ctx)
	if e != nil {
		s.skip("savepoint-staff", e)
		return nil
	}
	if e = s.emitStaffMessage(ctx, sp, a, org, v, now); e != nil {
		sp.Rollback(context.Background())
		s.skip("emit-staff", e)
		return nil
	}
	if e = sp.Commit(ctx); e != nil {
		s.skip("release-staff", e)
	}
	return nil
}

func (s *store) emitStaffMessage(ctx context.Context, tx pgx.Tx, a domain.Actor, org string, v domain.StaffMessage, now time.Time) error {
	var site string
	if e := tx.QueryRow(ctx, `SELECT name FROM locations WHERE organization_id=$1 AND id=$2`, org, v.LocationID).Scan(&site); e != nil {
		return e
	}
	recipients, e := supervisors(ctx, tx, org, v.LocationID, a.ID)
	if e != nil {
		return e
	}
	// Quien avisa va en el título («Aviso de Lucía: Falta insumo») para que el jefe sepa a quién responder.
	var sender string
	if e := tx.QueryRow(ctx, `SELECT COALESCE(display_name,'') FROM application_users WHERE id=$1`, a.ID).Scan(&sender); e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	title := truncateRunes(staffMessageTitle(sender, domain.StaffMessageLabel(v.Kind)), 160)
	message := site
	if v.Text != "" {
		message = site + " · " + v.Text
	}
	message = truncateRunes(message, 500)
	snapshot, e := json.Marshal(map[string]any{"emailEnabled": false, "pushEnabled": true, "kind": v.Kind, "configuration": map[string]any{}})
	if e != nil {
		return e
	}
	for _, user := range recipients {
		var id string
		e = tx.QueryRow(ctx, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,dedup_key,context_snapshot,created_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,'STAFF_MESSAGE',$7,$8,$9,$10,$11,$11) ON CONFLICT(organization_id,dedup_key) DO NOTHING RETURNING id::text`,
			org, v.LocationID, user, StaffMessageAlertType, title, message, v.ID, "staff-message:"+v.ID+":"+user, snapshot, now, a.ID).Scan(&id)
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

// SupervisorContact devuelve el WhatsApp del jefe de cocina activo de la sede o, si ninguno lo tiene, del administrador.
// Nunca devuelve el propio número de quien pregunta.
func (s *store) SupervisorContact(ctx context.Context, a domain.Actor, org, location string) (domain.SupervisorContact, error) {
	var c domain.SupervisorContact
	e := s.tx.QueryRow(ctx, `SELECT u.display_name,m.role,u.phone_number FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 WHERE m.organization_id=$1 AND m.status='ACTIVE' AND m.user_id<>$3 AND u.phone_number ~ '^[0-9]{8,15}$'
 AND (m.role='ADMIN' OR (m.role='CHEF' AND EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=m.organization_id AND ml.user_id=m.user_id AND ml.location_id=$2)))
 ORDER BY CASE m.role WHEN 'CHEF' THEN 0 ELSE 1 END, m.user_id LIMIT 1`, org, location, a.ID).Scan(&c.Name, &c.Role, &c.WhatsApp)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.SupervisorContact{}, nil
	}
	return c, e
}

// staffMessageTitle arma «Aviso de Lucía: Falta insumo»; sin nombre queda «Aviso del equipo: Falta insumo».
func staffMessageTitle(sender, label string) string {
	if name := strings.TrimSpace(sender); name != "" {
		return "Aviso de " + name + ": " + label
	}
	return "Aviso del equipo: " + label
}
