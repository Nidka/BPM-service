// Package application contiene los casos de uso del registro de uso (RF-27).
package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/usage/domain"
)

// Store es el conjunto de operaciones disponibles dentro de una transacción.
type Store interface {
	// Actor devuelve el rol desde la membresía activa (o SUPER_ADMIN); ErrDenied si no pertenece.
	Actor(ctx context.Context, user, org string) (domain.Actor, error)
	// EnsureSession crea la sesión si no existe; ErrSessionConflict si pertenece a otro usuario u organización.
	EnsureSession(ctx context.Context, org, user, session string, startedAt time.Time) error
	// InsertEvent devuelve false si el UUID ya existía.
	InsertEvent(ctx context.Context, org, user string, e domain.Event) (bool, error)
	// FindEvent devuelve el evento ya guardado con ese UUID.
	FindEvent(ctx context.Context, org, id string) (domain.Event, string, error)
	MarkSessionStart(ctx context.Context, org, session string, at time.Time) error
	MarkSessionEnd(ctx context.Context, org, session string, at time.Time) error
	Summary(ctx context.Context, org string, p domain.Period) (domain.Summary, error)
	// ResearchSummary agrega solo usuarios con participación ACTIVE y consentimiento de investigación vigente.
	ResearchSummary(ctx context.Context, org string, p domain.Period) (domain.Summary, error)
}

// UnitOfWork ejecuta fn en una transacción.
type UnitOfWork interface {
	Within(ctx context.Context, fn func(Store) error) error
}

// Service reúne los casos de uso.
type Service struct {
	work UnitOfWork
	now  func() time.Time
}

// New crea el servicio.
func New(work UnitOfWork) *Service { return &Service{work: work, now: time.Now} }

// Result cuenta los eventos aceptados y los repetidos (idempotentes).
type Result struct{ Accepted, Duplicates int }

// Record registra un lote de eventos. El usuario y la empresa salen de la identidad autenticada;
// el lote es atómico e idempotente por UUID de evento.
func (s *Service) Record(ctx context.Context, user, org string, batch []domain.Event) (Result, error) {
	var out Result
	err := s.work.Within(ctx, func(db Store) error {
		if !domain.ValidOrganization(org) {
			return domain.ErrDenied
		}
		a, err := db.Actor(ctx, user, org)
		if err != nil {
			return err
		}
		if !a.CanRecord() {
			return domain.ErrDenied
		}
		events, err := domain.NormalizeBatch(batch, s.now())
		if err != nil {
			return err
		}
		for _, e := range events {
			if err = db.EnsureSession(ctx, org, user, e.SessionID, e.OccurredAt); err != nil {
				return err
			}
			inserted, err := db.InsertEvent(ctx, org, user, e)
			if err != nil {
				return err
			}
			if !inserted {
				prev, owner, err := db.FindEvent(ctx, org, e.ID)
				if err != nil {
					return err
				}
				if owner != user || !prev.Same(e) {
					return domain.ErrIdempotencyConflict
				}
				out.Duplicates++
				continue
			}
			out.Accepted++
			switch e.Type {
			case domain.SessionStart:
				err = db.MarkSessionStart(ctx, org, e.SessionID, e.OccurredAt)
			case domain.SessionEnd:
				err = db.MarkSessionEnd(ctx, org, e.SessionID, e.OccurredAt)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// Summary devuelve el uso agregado de la empresa (ADMIN y SUPER_ADMIN).
func (s *Service) Summary(ctx context.Context, user, org, from, to string) (domain.Summary, error) {
	var out domain.Summary
	err := s.work.Within(ctx, func(db Store) error {
		if !domain.ValidOrganization(org) {
			return domain.ErrDenied
		}
		a, err := db.Actor(ctx, user, org)
		if err != nil {
			return err
		}
		if !a.CanQuery() {
			return domain.ErrDenied
		}
		p, err := domain.ParsePeriod(from, to)
		if err != nil {
			return err
		}
		out, err = db.Summary(ctx, org, p)
		return err
	})
	return out, err
}

// ResearchSummary devuelve el uso agregado destinado a investigación (ADMIN y SUPER_ADMIN),
// limitado a usuarios con participación y consentimiento de investigación vigentes.
func (s *Service) ResearchSummary(ctx context.Context, user, org, from, to string) (domain.Summary, error) {
	var out domain.Summary
	err := s.work.Within(ctx, func(db Store) error {
		if !domain.ValidOrganization(org) {
			return domain.ErrDenied
		}
		a, err := db.Actor(ctx, user, org)
		if err != nil {
			return err
		}
		if !a.CanQuery() {
			return domain.ErrDenied
		}
		p, err := domain.ParsePeriod(from, to)
		if err != nil {
			return err
		}
		out, err = db.ResearchSummary(ctx, org, p)
		return err
	})
	return out, err
}
