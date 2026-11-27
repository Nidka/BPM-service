// Package application implementa la lectura y edición del contacto de soporte.
package application

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/platformsettings/domain"
)

// Store es la persistencia de los ajustes de plataforma.
type Store interface {
	// PlatformRole devuelve el rol de plataforma de una persona ACTIVE; ErrDenied si no está activa.
	PlatformRole(ctx context.Context, user string) (string, error)
	SupportContact(ctx context.Context) (domain.SupportContact, error)
	SaveSupportContact(ctx context.Context, user string, contact domain.SupportContact) (domain.SupportContact, error)
}

// Service orquesta permisos y validación.
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

// SupportContact lo puede leer cualquier persona activa.
func (s *Service) SupportContact(ctx context.Context, user string) (domain.SupportContact, error) {
	if _, err := s.store.PlatformRole(ctx, user); err != nil {
		return domain.SupportContact{}, err
	}
	return s.store.SupportContact(ctx)
}

// UpdateSupportContact solo lo puede hacer el administrador general (SUPER_ADMIN).
func (s *Service) UpdateSupportContact(ctx context.Context, user, email, whatsapp string) (domain.SupportContact, error) {
	role, err := s.store.PlatformRole(ctx, user)
	if err != nil {
		return domain.SupportContact{}, err
	}
	if role != "SUPER_ADMIN" {
		return domain.SupportContact{}, domain.ErrDenied
	}
	contact, err := domain.NormalizeSupportContact(email, whatsapp)
	if err != nil {
		return domain.SupportContact{}, err
	}
	return s.store.SaveSupportContact(ctx, user, contact)
}
