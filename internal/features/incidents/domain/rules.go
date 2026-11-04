package domain

import "time"

// Allowed deriva las opciones del usuario; Apply siempre vuelve a comprobarlas.
func Allowed(v Incident, a Actor) []string {
	out := []string{}
	if v.Status == "OPEN" && len(v.Actions) == 0 && CanSupervise(a.Role) {
		return append(out, "ASSIGN")
	}
	if len(v.Actions) != 1 {
		return out
	}
	x := v.Actions[0]
	if x.ResponsibleUserID == a.ID && CanReport(a.Role) {
		if v.Status == "ASSIGNED" && x.Status == "PENDING" {
			out = append(out, "START")
		}
		if v.Status == "IN_PROGRESS" && x.Status == "IN_PROGRESS" {
			out = append(out, "RESOLVE")
		}
	}
	if v.Status == "RESOLVED" && x.Status == "RESOLVED" && CanSupervise(a.Role) && x.ResponsibleUserID != a.ID {
		out = append(out, "VERIFY", "RETURN")
	}
	return out
}
func Apply(v *Incident, a Actor, c Command, now time.Time) error {
	if v.RowVersion != c.RowVersion {
		return ErrConflict
	}
	if c.ActionType == "ASSIGN" && !CanSupervise(a.Role) {
		return ErrDenied
	}
	if c.ActionType == "START" || c.ActionType == "RESOLVE" {
		if len(v.Actions) == 1 && (v.Actions[0].ResponsibleUserID != a.ID || !CanReport(a.Role)) {
			return ErrDenied
		}
	}
	if c.ActionType == "VERIFY" || c.ActionType == "RETURN" {
		if !CanSupervise(a.Role) || (len(v.Actions) == 1 && v.Actions[0].ResponsibleUserID == a.ID) {
			return ErrDenied
		}
	}
	allowed := false
	for _, x := range Allowed(*v, a) {
		if x == c.ActionType {
			allowed = true
		}
	}
	if !allowed {
		return ErrConflict
	}
	switch c.ActionType {
	case "ASSIGN":
		if !c.DueAt.After(now) {
			return invalid("dueAt")
		}
		v.Actions = []Action{{ID: c.ActionID, ResponsibleUserID: c.ResponsibleUserID, Description: c.Description, DueAt: c.DueAt, Status: "PENDING"}}
		v.Status = "ASSIGNED"
	case "START":
		v.Status = "IN_PROGRESS"
		v.Actions[0].Status = "IN_PROGRESS"
	case "RESOLVE":
		v.Status = "RESOLVED"
		v.Actions[0].Status = "RESOLVED"
		v.Actions[0].ResolutionNote = c.Note
		v.Actions[0].ResolvedAt = &now
	case "RETURN":
		v.Status = "IN_PROGRESS"
		v.Actions[0].Status = "IN_PROGRESS"
		v.Actions[0].ResolvedAt = nil
	case "VERIFY":
		if v.EvidenceRequired && (v.BeforeCount < 1 || v.AfterCount < 1) {
			return ErrConflict
		}
		v.Status = "CLOSED"
		v.ClosedAt = &now
		v.Actions[0].Status = "VERIFIED"
		v.Actions[0].VerifiedAt = &now
		v.Actions[0].VerifiedBy = &a.ID
	}
	v.RowVersion++
	return nil
}
