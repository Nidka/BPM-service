package application

import (
	"context"
	"io"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// Service reúne los casos de uso del módulo de investigación.
type Service struct {
	work   UnitOfWork
	now    func() time.Time
	random io.Reader
}

// New crea el servicio.
func New(work UnitOfWork) *Service { return &Service{work: work, now: time.Now} }

// researcher autoriza una operación de investigación: exige la concesión de
// plataforma vigente. Ningún rol de empresa (ni siquiera ADMIN) la sustituye.
func (s *Service) researcher(ctx context.Context, db Store, user string) error {
	ok, err := db.IsResearcher(ctx, user)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrDenied
	}
	return nil
}

// asResearcher ejecuta fn como investigador y audita el acceso en la misma transacción.
func (s *Service) asResearcher(ctx context.Context, user, org, action, entityType, entityID string, meta map[string]string, fn func(Store) error) error {
	return s.work.Within(ctx, func(db Store) error {
		if err := s.researcher(ctx, db, user); err != nil {
			return err
		}
		if err := fn(db); err != nil {
			return err
		}
		return db.Audit(ctx, org, user, action, entityType, entityID, meta)
	})
}

// Access informa qué puede hacer el usuario autenticado en el módulo.
func (s *Service) Access(ctx context.Context, user string) (canResearch, canManageGrants bool, err error) {
	err = s.work.Within(ctx, func(db Store) error {
		var e error
		if canResearch, e = db.IsResearcher(ctx, user); e != nil {
			return e
		}
		canManageGrants, e = db.IsSuperAdmin(ctx, user)
		return e
	})
	return
}

// validUUIDCursor rechaza cursores cuyo ID no sea un UUID (defensa ante cursores manipulados).
func validUUIDCursor(w pagination.Window) error {
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return pagination.ErrInvalid
	}
	return nil
}
