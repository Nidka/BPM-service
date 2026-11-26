package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// AssignedAlertType es el tipo de aviso que recibe el responsable de una acción correctiva recién asignada.
const AssignedAlertType = "INCIDENT_ASSIGNED"

// avisoNoDisponibleCada limita el registro del aviso «esquema sin V21» para no inundar los logs.
const avisoNoDisponibleCada = 10 * time.Minute

var ultimoAvisoNoDisponible atomic.Int64

// WithLogger asigna el logger donde se registran (sin datos personales) los avisos omitidos.
func (r *Repository) WithLogger(logger *slog.Logger) *Repository {
	r.logger = logger
	return r
}

// assignedAlertsSupported indica si la base ya tiene V21 (función marcadora). Sin ella el aviso de asignación no
// puede autorizarse ni entregarse, así que el emisor no lo crea.
func assignedAlertsSupported(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT to_regprocedure('alert_incident_assigned_supported()') IS NOT NULL`).Scan(&ok)
	return ok, e
}

// NotifyAssigned crea, en la misma transacción de ASSIGN, el aviso del responsable (bandeja + evento de salida para
// el worker de push). Nunca debe romper la asignación: se ejecuta en un savepoint y cualquier fallo se registra y se
// omite. Sin V21 se omite con una advertencia (despliegue del código antes de la migración). No notifica a quien se
// asigna a sí mismo. El texto no incluye datos de personas: categoría, sede y plazo en la zona de la sede.
func (s *store) NotifyAssigned(ctx context.Context, actor domain.Actor, org string, v domain.Incident, now time.Time) error {
	if len(v.Actions) != 1 {
		return nil
	}
	x := v.Actions[0]
	if x.ResponsibleUserID == "" || x.ResponsibleUserID == actor.ID {
		return nil
	}
	ok, e := assignedAlertsSupported(ctx, s.tx)
	if e != nil {
		s.skip("probe", e)
		return nil
	}
	if !ok {
		s.warnUnsupported(now)
		return nil
	}
	sp, e := s.tx.Begin(ctx)
	if e != nil {
		s.skip("savepoint", e)
		return nil
	}
	if e = s.emitAssigned(ctx, sp, org, v, x, now); e != nil {
		sp.Rollback(context.Background())
		s.skip("emit", e)
		return nil
	}
	if e = sp.Commit(ctx); e != nil {
		s.skip("release", e)
	}
	return nil
}

func (s *store) emitAssigned(ctx context.Context, tx pgx.Tx, org string, v domain.Incident, x domain.Action, now time.Time) error {
	var site, zone string
	if e := tx.QueryRow(ctx, `SELECT name,timezone FROM locations WHERE organization_id=$1 AND id=$2`, org, v.LocationID).Scan(&site, &zone); e != nil {
		return e
	}
	title := truncateRunes("Te asignaron una incidencia: "+v.Name, 160)
	message := truncateRunes(site+" · Plazo "+localDeadline(x.DueAt, zone), 500)
	snapshot, e := json.Marshal(map[string]any{"actionId": x.ID, "emailEnabled": false, "pushEnabled": true, "configuration": map[string]any{}})
	if e != nil {
		return e
	}
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,dedup_key,context_snapshot,created_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,'INCIDENT',$7,$8,$9,$10,$11,$11) ON CONFLICT(organization_id,dedup_key) DO NOTHING RETURNING id::text`,
		org, v.LocationID, x.ResponsibleUserID, AssignedAlertType, title, message, v.ID, "incident-assigned:"+x.ID, snapshot, now, s.actor.ID).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	return outbox.Enqueue(ctx, tx, outbox.Event{OrganizationID: org, Type: "notifications.alert.ready.v1", AggregateType: "alert", AggregateID: id, DedupKey: "alert:" + id, Payload: json.RawMessage(`{}`), CreatedAt: now, ExpiresAt: now.Add(23 * time.Hour)})
}

func (s *store) warnUnsupported(now time.Time) {
	last := ultimoAvisoNoDisponible.Load()
	if s.logger == nil || now.UnixNano()-last < int64(avisoNoDisponibleCada) || !ultimoAvisoNoDisponible.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	s.logger.Warn("aviso de incidencia asignada omitido: falta la migración V21", "alertType", AssignedAlertType)
}

// skip registra solo el contexto y el código SQLSTATE; nunca el texto del error ni datos del aviso.
func (s *store) skip(stage string, e error) {
	if s.logger == nil {
		return
	}
	code := ""
	var pg *pgconn.PgError
	if errors.As(e, &pg) {
		code = pg.Code
	}
	s.logger.Warn("aviso de incidencia asignada omitido", "alertType", AssignedAlertType, "stage", stage, "sqlstate", code)
}

// localDeadline formatea el plazo como dd/MM/aaaa HH:mm en la zona de la sede (UTC si la zona no existe).
func localDeadline(due time.Time, zone string) string {
	loc, e := time.LoadLocation(zone)
	if e != nil {
		loc = time.UTC
	}
	return due.In(loc).Format("02/01/2006 15:04")
}

func truncateRunes(v string, max int) string {
	if utf8.RuneCountInString(v) <= max {
		return v
	}
	return strings.TrimSpace(string([]rune(v)[:max-1])) + "…"
}
