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
	sole, e := withSoleValidator(ctx, db, a, org, v)
	if e != nil {
		return v, e
	}
	v.AllowedActions = domain.Allowed(v, sole)
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

// withSoleValidator marca al actor cuando es responsable de la única acción resuelta y
// no existe otra persona elegible para validarla (autovalidación con nota).
func withSoleValidator(ctx context.Context, db Store, a domain.Actor, org string, v domain.Incident) (domain.Actor, error) {
	if v.Status != "RESOLVED" || len(v.Actions) != 1 || v.Actions[0].ResponsibleUserID != a.ID || !domain.CanSupervise(a.Role) {
		return a, nil
	}
	other, e := db.OtherValidator(ctx, org, v.LocationID, a.ID)
	a.SoleValidator = e == nil && !other
	return a, e
}
