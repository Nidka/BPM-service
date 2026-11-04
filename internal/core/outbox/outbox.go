// Package outbox conserva eventos técnicos en la transacción que origina el cambio.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrUnavailable = errors.New("proveedor no configurado")

// Event no contiene credenciales ni información privada del recurso original.
type Event struct {
	OrganizationID, Type, AggregateType, AggregateID, DedupKey, ActorID string
	Payload                                                             json.RawMessage
	CreatedAt, ExpiresAt                                                time.Time
}
type Claimed struct {
	Event
	ID, LeaseToken string
	Attempts       int
}

func Enqueue(ctx context.Context, tx pgx.Tx, v Event) error {
	if v.OrganizationID == "" || v.Type == "" || v.AggregateType == "" || v.AggregateID == "" || v.DedupKey == "" {
		return errors.New("evento incompleto")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.ExpiresAt.IsZero() {
		v.ExpiresAt = v.CreatedAt.Add(23 * time.Hour)
	}
	if !v.ExpiresAt.After(v.CreatedAt) || v.ExpiresAt.Sub(v.CreatedAt) >= 24*time.Hour {
		return errors.New("vigencia de evento inválida")
	}
	if len(v.Payload) == 0 {
		v.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(v.Payload) {
		return errors.New("payload inválido")
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(organization_id,event_type,aggregate_type,aggregate_id,payload,dedup_key,created_at,available_at,expires_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$8,NULLIF($9,''),NULLIF($9,'')) ON CONFLICT(organization_id,dedup_key) DO NOTHING`, v.OrganizationID, v.Type, v.AggregateType, v.AggregateID, []byte(v.Payload), v.DedupKey, v.CreatedAt, v.ExpiresAt, v.ActorID)
	return err
}
