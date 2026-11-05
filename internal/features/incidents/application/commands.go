package application

import (
	"context"
	"encoding/json"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"strings"
	"time"
)

func (s *Service) Report(ctx context.Context, user, org, key string, input domain.Report) (domain.Incident, error) {
	v, e := domain.NormalizeReport(input, s.now())
	if e != nil {
		return domain.Incident{}, e
	}
	return write(ctx, s, user, org, "report", key, v.ClientOperationID, v, func(db Store, a domain.Actor) error {
		if !domain.CanReport(a.Role) {
			return domain.ErrDenied
		}
		return db.Location(ctx, a, org, v.LocationID)
	}, func(db Store, a domain.Actor, hash string) (domain.Incident, error) {
		if _, e := db.Catalog(ctx, org, v.CatalogID); e != nil {
			return domain.Incident{}, e
		}
		out, e := db.InsertIncident(ctx, a, org, v, hash)
		if e != nil {
			return out, e
		}
		out.AllowedActions = domain.Allowed(out, a)
		body, e := json.Marshal(out)
		if e != nil {
			return out, e
		}
		if e = db.Event(ctx, a, org, out.ID, v.ClientOperationID, "", "OPEN", "", hash, body); e != nil {
			return out, e
		}
		return out, db.Audit(ctx, a, org, "incident.reported", out.ID)
	})
}
func (s *Service) Act(ctx context.Context, user, org, id, key string, input domain.Command) (domain.Incident, error) {
	id = strings.ToLower(id)
	if !domain.ValidID(id) {
		return domain.Incident{}, domain.ErrNotFound
	}
	c, e := domain.NormalizeCommand(input, s.now())
	if e != nil {
		return domain.Incident{}, e
	}
	var current domain.Incident
	return write(ctx, s, user, org, "action:"+id, key, c.OperationID, c, func(db Store, a domain.Actor) error {
		var e error
		current, e = authorizeIncident(ctx, db, a, org, id, true)
		if e != nil {
			return e
		}
		switch c.ActionType {
		case "ASSIGN", "VERIFY", "RETURN":
			if !domain.CanSupervise(a.Role) || ((c.ActionType == "VERIFY" || c.ActionType == "RETURN") && len(current.Actions) == 1 && current.Actions[0].ResponsibleUserID == a.ID) {
				return domain.ErrDenied
			}
		case "START", "RESOLVE":
			if len(current.Actions) == 1 && (current.Actions[0].ResponsibleUserID != a.ID || !domain.CanReport(a.Role)) {
				return domain.ErrDenied
			}
		}
		return nil
	}, func(db Store, a domain.Actor, hash string) (domain.Incident, error) {
		// La lectura bloqueó el incidente; media toma el mismo bloqueo antes de mutar fotos.
		if c.ActionType == "ASSIGN" {
			if e := db.Eligible(ctx, org, current.LocationID, c.ResponsibleUserID); e != nil {
				return current, e
			}
		}
		from := current.Status
		version := current.RowVersion
		if e := domain.Apply(&current, a, c, s.now().UTC().Truncate(time.Microsecond)); e != nil {
			return current, e
		}
		if e := db.SaveIncident(ctx, a, org, current, version); e != nil {
			return current, e
		}
		out, e := db.Incident(ctx, org, id, false)
		if e != nil {
			return out, e
		}
		out.AllowedActions = domain.Allowed(out, a)
		body, e := json.Marshal(out)
		if e != nil {
			return out, e
		}
		if e = db.Event(ctx, a, org, id, c.OperationID, from, out.Status, c.Note, hash, body); e != nil {
			return out, e
		}
		return out, db.Audit(ctx, a, org, "incident."+c.ActionType, id)
	})
}
