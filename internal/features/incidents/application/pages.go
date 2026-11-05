package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"strings"
	"time"
)

func list[T any](ctx context.Context, s *Service, user, org, resource, site, id, status string, r pagination.Request, authorize func(Store, domain.Actor) error, fetch func(Store, domain.Actor, pagination.Window) ([]T, error), position func(T) pagination.Position) (out pagination.Page[T], err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrNotFound
	}
	scope := []string{user, org, "incidents", resource, site, id, status}
	w, e := pagination.Open(r, scope...)
	if e != nil {
		return out, e
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if !domain.CanRead(a.Role) {
			return domain.ErrDenied
		}
		if e = authorize(db, a); e != nil {
			return e
		}
		rows, e := fetch(db, a, w)
		if e != nil {
			return e
		}
		out = pagination.Close(rows, w, position, scope...)
		return nil
	})
	return
}
func (s *Service) Catalogs(ctx context.Context, user, org string, r pagination.Request) (pagination.Page[domain.Catalog], error) {
	return list(ctx, s, user, org, "catalog", "", "", "", r, func(Store, domain.Actor) error { return nil }, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Catalog, error) {
		return db.Catalogs(ctx, org, w)
	}, func(v domain.Catalog) pagination.Position {
		return pagination.Position{Key: strings.ToLower(v.Name), ID: v.ID}
	})
}
func (s *Service) Incidents(ctx context.Context, user, org, site, status string, r pagination.Request) (pagination.Page[domain.Incident], error) {
	site = strings.ToLower(site)
	if !domain.ValidID(site) || (status != "" && !domain.ValidStatus(status)) {
		return pagination.Page[domain.Incident]{}, pagination.ErrInvalid
	}
	return list(ctx, s, user, org, "incidents", site, "", status, r, func(db Store, a domain.Actor) error { return db.Location(ctx, a, org, site) }, func(db Store, a domain.Actor, w pagination.Window) ([]domain.Incident, error) {
		return db.Incidents(ctx, a, org, site, status, w)
	}, func(v domain.Incident) pagination.Position {
		return pagination.Position{Key: v.CreatedAt.UTC().Format(time.RFC3339Nano), ID: v.ID}
	})
}
func (s *Service) Assignees(ctx context.Context, user, org, id string, r pagination.Request) (pagination.Page[domain.Assignee], error) {
	id = strings.ToLower(id)
	if !domain.ValidID(id) {
		return pagination.Page[domain.Assignee]{}, domain.ErrNotFound
	}
	var site string
	return list(ctx, s, user, org, "assignees", "", id, "", r, func(db Store, a domain.Actor) error {
		if !domain.CanSupervise(a.Role) {
			return domain.ErrDenied
		}
		v, e := authorizeIncident(ctx, db, a, org, id, false)
		site = v.LocationID
		return e
	}, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Assignee, error) {
		return db.Assignees(ctx, org, site, w)
	}, func(v domain.Assignee) pagination.Position {
		return pagination.Position{Key: strings.ToLower(v.DisplayName), ID: v.ID}
	})
}
func (s *Service) History(ctx context.Context, user, org, id string, r pagination.Request) (pagination.Page[domain.Event], error) {
	id = strings.ToLower(id)
	if !domain.ValidID(id) {
		return pagination.Page[domain.Event]{}, domain.ErrNotFound
	}
	if r.Query != "" {
		return pagination.Page[domain.Event]{}, pagination.ErrInvalid
	}
	return list(ctx, s, user, org, "history", "", id, "", r, func(db Store, a domain.Actor) error { _, e := authorizeIncident(ctx, db, a, org, id, false); return e }, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Event, error) {
		return db.History(ctx, org, id, w)
	}, func(v domain.Event) pagination.Position {
		return pagination.Position{Key: v.CreatedAt.UTC().Format(time.RFC3339Nano), ID: v.ID}
	})
}
