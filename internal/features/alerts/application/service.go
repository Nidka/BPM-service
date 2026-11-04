// Package application coordina reglas, bandeja privada y evaluación de alertas.
package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/alerts/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"strings"
	"time"
)

type Store interface {
	WriteRule(context.Context, string, string, string, domain.Rule, bool) (domain.Rule, error)
	Rules(context.Context, string, string, string, pagination.Request) (pagination.Page[domain.Rule], error)
	Inbox(context.Context, string, string, string, pagination.Request) (pagination.Page[domain.Alert], error)
	Detail(context.Context, string, string, string) (domain.Alert, error)
	Count(context.Context, string, string) (int, error)
	Transition(context.Context, string, string, string, bool) (domain.Alert, error)
	EvaluationRules(context.Context, string, string, string) ([]string, error)
	EvaluateRule(context.Context, string, string, time.Time) error
}
type UnitOfWork interface {
	Within(context.Context, func(Store) error) error
}
type Service struct{ repo UnitOfWork }

func New(r UnitOfWork) *Service { return &Service{r} }
func (s *Service) WriteRule(ctx context.Context, user, org, key string, v domain.Rule, create bool) (domain.Rule, error) {
	var e error
	if !domain.ValidUID(user) || !domain.ValidOrganization(org) {
		return v, domain.ErrDenied
	}
	if len(key) < 8 || len(key) > 100 || strings.TrimSpace(key) != key {
		return v, domain.ErrInvalid
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return v, domain.ErrInvalid
		}
	}
	v, e = domain.NormalizeRule(v, create)
	if e != nil {
		return v, e
	}
	return transact(ctx, s.repo, func(store Store) (domain.Rule, error) { return store.WriteRule(ctx, user, org, key, v, create) })
}
func (s *Service) Rules(ctx context.Context, u, o, l string, p pagination.Request) (pagination.Page[domain.Rule], error) {
	if l != "" && !domain.ValidID(l) {
		return pagination.Page[domain.Rule]{}, domain.ErrInvalid
	}
	return transact(ctx, s.repo, func(store Store) (pagination.Page[domain.Rule], error) { return store.Rules(ctx, u, o, l, p) })
}
func (s *Service) Inbox(ctx context.Context, u, o, state string, p pagination.Request) (pagination.Page[domain.Alert], error) {
	if state == "" {
		state = "ALL"
	}
	switch state {
	case "ALL", "UNREAD", "VIEWED", "ATTENDED":
	default:
		return pagination.Page[domain.Alert]{}, domain.ErrInvalid
	}
	return transact(ctx, s.repo, func(store Store) (pagination.Page[domain.Alert], error) { return store.Inbox(ctx, u, o, state, p) })
}
func (s *Service) Detail(ctx context.Context, u, o, id string) (domain.Alert, error) {
	if !domain.ValidID(id) {
		return domain.Alert{}, domain.ErrNotFound
	}
	return transact(ctx, s.repo, func(store Store) (domain.Alert, error) { return store.Detail(ctx, u, o, id) })
}
func (s *Service) Count(ctx context.Context, u, o string) (int, error) {
	return transact(ctx, s.repo, func(store Store) (int, error) { return store.Count(ctx, u, o) })
}
func (s *Service) Transition(ctx context.Context, u, o, id string, attend bool) (domain.Alert, error) {
	if !domain.ValidID(id) {
		return domain.Alert{}, domain.ErrNotFound
	}
	return transact(ctx, s.repo, func(store Store) (domain.Alert, error) { return store.Transition(ctx, u, o, id, attend) })
}
func (s *Service) EvaluateReceipt(ctx context.Context, o, id string, now time.Time) error {
	if !domain.ValidOrganization(o) || !domain.ValidID(id) {
		return domain.ErrInvalid
	}
	return s.evaluate(ctx, o, id, now)
}
func (s *Service) EvaluateScheduled(ctx context.Context, now time.Time) error {
	return s.evaluate(ctx, "", "", now)
}

// evaluate limita los bloqueos a una regla y pagina la selección para no retener todos los tenants.
func (s *Service) evaluate(ctx context.Context, org, receipt string, now time.Time) error {
	after := ""
	for {
		ids, e := transact(ctx, s.repo, func(store Store) ([]string, error) { return store.EvaluationRules(ctx, org, receipt, after) })
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if e = s.repo.Within(ctx, func(store Store) error { return store.EvaluateRule(ctx, id, receipt, now) }); e != nil {
				return e
			}
			after = id
		}
	}
}

func transact[T any](ctx context.Context, uow UnitOfWork, fn func(Store) (T, error)) (v T, e error) {
	e = uow.Within(ctx, func(store Store) error { var err error; v, err = fn(store); return err })
	return
}
