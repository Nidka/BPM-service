package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
)

// SendStaffMessage es «Avisar a mi jefe»: guarda el aviso y avisa (bandeja + push) al jefe de cocina y al administrador.
// Idempotente por Idempotency-Key y por id.
func (s *Service) SendStaffMessage(ctx context.Context, user, org, key string, input domain.StaffMessage) (domain.StaffMessage, error) {
	v, e := domain.NormalizeStaffMessage(input)
	if e != nil {
		return domain.StaffMessage{}, e
	}
	return write(ctx, s, user, org, "staff-message", key, "", v, func(db Store, a domain.Actor) error {
		if !domain.CanReport(a.Role) {
			return domain.ErrDenied
		}
		return db.Location(ctx, a, org, v.LocationID)
	}, func(db Store, a domain.Actor, _ string) (domain.StaffMessage, error) {
		now := s.now().UTC().Truncate(time.Microsecond)
		out, e := db.InsertStaffMessage(ctx, a, org, v, now)
		if e != nil {
			return out, e
		}
		if e = db.Audit(ctx, a, org, "staff_message.sent", out.ID); e != nil {
			return out, e
		}
		return out, db.NotifyStaffMessage(ctx, a, org, out, now)
	})
}

// SupervisorContact devuelve a quién escribir por WhatsApp desde una sede (vacío si nadie dejó su número).
func (s *Service) SupervisorContact(ctx context.Context, user, org, location string) (out domain.SupervisorContact, err error) {
	if !domain.ValidOrganization(org) || !domain.ValidID(location) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if !domain.CanReport(a.Role) {
			return domain.ErrDenied
		}
		if e = db.Location(ctx, a, org, location); e != nil {
			return e
		}
		out, e = db.SupervisorContact(ctx, a, org, location)
		return e
	})
	return
}
