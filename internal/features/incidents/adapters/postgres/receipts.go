package postgres

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/jackc/pgx/v5"
)

func (s *store) ReplayOperation(ctx context.Context, org, operation, hash string) ([]byte, bool, error) {
	var saved, actor string
	var body []byte
	e := s.tx.QueryRow(ctx, `SELECT request_hash,response_body,actor_user_id FROM incident_events WHERE organization_id=$1 AND operation_id=$2`, org, operation).Scan(&saved, &body, &actor)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	if saved != hash || actor != s.actor.ID || len(body) == 0 {
		return nil, false, domain.ErrConflict
	}
	return body, true, nil
}
func (s *store) Event(ctx context.Context, a domain.Actor, org, id, operation, from, to, note, hash string, body []byte) error {
	_, e := s.tx.Exec(ctx, `INSERT INTO incident_events(organization_id,incident_id,operation_id,from_status,to_status,actor_user_id,note,request_hash,response_body) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9)`, org, id, operation, from, to, a.ID, note, hash, body)
	return conflict(e)
}
