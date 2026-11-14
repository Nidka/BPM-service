package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Outbox implementa application.InvitationOutbox para el worker.
type Outbox struct{ pool *pgxpool.Pool }

func NewOutbox(pool *pgxpool.Pool) *Outbox { return &Outbox{pool} }

func (o *Outbox) Current(ctx context.Context, org, id, tokenHash string) (ok bool, err error) {
	err = o.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_invitations WHERE organization_id=$1 AND id::text=$2 AND token_hash=$3 AND status='PENDING' AND expires_at>NOW())`, org, id, tokenHash).Scan(&ok)
	return
}

func (o *Outbox) Scrub(ctx context.Context, org, id string) error {
	_, err := o.pool.Exec(ctx, `UPDATE outbox_events SET payload='{"scrubbed":true}'::jsonb,updated_at=NOW() WHERE organization_id=$1 AND aggregate_type='organization_invitation' AND aggregate_id=$2 AND event_type=$3 AND status IN ('PENDING','PROCESSING')`, org, id, InvitationEvent)
	return err
}
