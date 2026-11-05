package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
)

func authorizeIncident(ctx context.Context, db Store, a domain.Actor, org, id string, lock bool) (domain.Incident, error) {
	v, e := db.Incident(ctx, org, id, lock)
	if e != nil {
		return v, e
	}
	if !domain.CanRead(a.Role) {
		return v, domain.ErrDenied
	}
	if e = db.Location(ctx, a, org, v.LocationID); e != nil {
		return v, e
	}
	if a.Role == "WORKER" && v.ReportedBy != a.ID {
		assigned := false
		for _, x := range v.Actions {
			if x.ResponsibleUserID == a.ID {
				assigned = true
			}
		}
		if !assigned {
			return v, domain.ErrDenied
		}
	}
	v.AllowedActions = domain.Allowed(v, a)
	return v, nil
}
func (s *Service) Detail(ctx context.Context, user, org, id string) (out domain.Incident, err error) {
	if !domain.ValidOrganization(org) || !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		out, e = authorizeIncident(ctx, db, a, org, id, false)
		return e
	})
	return
}
