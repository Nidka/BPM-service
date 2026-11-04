package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
)

func validKey(k string) bool {
	if len(k) < 8 || len(k) > 100 {
		return false
	}
	for _, c := range k {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func requestHash(user, org, kind string, v any) (string, error) {
	b, e := json.Marshal(struct {
		Actor, Organization, Kind string
		Input                     any
	}{user, org, kind, v})
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type authorizer func(Store, domain.Actor) error

// write reautoriza antes de ambos tipos de replay y serializa los recibos por tenant.
func write[T any](ctx context.Context, s *Service, user, org, kind, key, operation string, input any, authorize authorizer, perform func(Store, domain.Actor, string) (T, error)) (out T, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrNotFound
	}
	if !validKey(key) {
		return out, &domain.ValidationError{Fields: map[string]string{"idempotencyKey": "Conserva la clave original de esta operación."}}
	}
	hash, e := requestHash(user, org, kind, input)
	if e != nil {
		return out, e
	}
	key = "incidents:" + key
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if e = db.Lock(ctx, org); e != nil {
			return e
		}
		if e = authorize(db, a); e != nil {
			return e
		}
		body, found, e := db.Replay(ctx, org, key, hash)
		if e != nil {
			return e
		}
		if found {
			return json.Unmarshal(body, &out)
		}
		if operation != "" {
			body, found, e = db.ReplayOperation(ctx, org, operation, hash)
			if e != nil {
				return e
			}
			if found {
				if e = json.Unmarshal(body, &out); e != nil {
					return e
				}
				return db.Remember(ctx, a, org, key, hash, body)
			}
		}
		out, e = perform(db, a, hash)
		if e != nil {
			return e
		}
		body, e = json.Marshal(out)
		if e != nil {
			return e
		}
		return db.Remember(ctx, a, org, key, hash, body)
	})
	return
}
