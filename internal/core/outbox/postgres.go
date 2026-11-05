package outbox

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(p *pgxpool.Pool) *Postgres { return &Postgres{p} }
func (q *Postgres) Claim(ctx context.Context, types []string, now time.Time, n int, lease time.Duration) ([]Claimed, error) {
	_, e := q.pool.Exec(ctx, `UPDATE outbox_events SET status='FAILED',last_error='EXPIRED',lease_token=NULL,locked_until=NULL WHERE event_type=ANY($1) AND (status='PENDING' OR(status='PROCESSING' AND locked_until<=$2)) AND (COALESCE(expires_at,created_at+interval '23 hours')<=$2 OR attempts>=8)`, types, now)
	if e != nil {
		return nil, e
	}
	rows, e := q.pool.Query(ctx, `WITH selected AS(SELECT id FROM outbox_events WHERE event_type=ANY($1) AND attempts<8 AND COALESCE(expires_at,created_at+interval '23 hours')>$2 AND ((status='PENDING' AND available_at<=$2)OR(status='PROCESSING' AND locked_until<=$2)) ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT $3) UPDATE outbox_events e SET status='PROCESSING',attempts=attempts+1,lease_token=gen_random_uuid(),locked_until=$4 FROM selected s WHERE e.id=s.id RETURNING e.id::text,e.organization_id,e.event_type,e.aggregate_type,e.aggregate_id,COALESCE(e.dedup_key,''),e.payload,COALESCE(e.created_by,''),e.created_at,COALESCE(e.expires_at,e.created_at+interval '23 hours'),e.lease_token::text,e.attempts`, types, now, n, now.Add(lease))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Claimed{}
	for rows.Next() {
		var v Claimed
		if e = rows.Scan(&v.ID, &v.OrganizationID, &v.Type, &v.AggregateType, &v.AggregateID, &v.DedupKey, &v.Payload, &v.ActorID, &v.CreatedAt, &v.ExpiresAt, &v.LeaseToken, &v.Attempts); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (q *Postgres) Complete(ctx context.Context, v Claimed, now time.Time) error {
	_, e := q.pool.Exec(ctx, `UPDATE outbox_events SET status='PROCESSED',processed_at=$3,lease_token=NULL,locked_until=NULL,last_error=NULL WHERE id=$1 AND lease_token=$2 AND status='PROCESSING' AND locked_until>$3`, v.ID, v.LeaseToken, now)
	return e
}
func (q *Postgres) Retry(ctx context.Context, v Claimed, at time.Time, code string, terminal bool) error {
	status := "PENDING"
	if terminal {
		status = "FAILED"
	}
	_, e := q.pool.Exec(ctx, `UPDATE outbox_events SET status=$3,available_at=$4,last_error=$5,lease_token=NULL,locked_until=NULL WHERE id=$1 AND lease_token=$2 AND status='PROCESSING'`, v.ID, v.LeaseToken, status, at, code)
	return e
}
func (q *Postgres) Defer(ctx context.Context, v Claimed, at time.Time) error {
	_, e := q.pool.Exec(ctx, `UPDATE outbox_events SET status='PENDING',attempts=GREATEST(0,attempts-1),available_at=$3,last_error='PROVIDER_UNAVAILABLE',lease_token=NULL,locked_until=NULL WHERE id=$1 AND lease_token=$2 AND status='PROCESSING'`, v.ID, v.LeaseToken, at)
	return e
}
