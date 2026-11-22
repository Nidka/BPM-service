// Package application implementa los casos de uso del buscador de normativa (RF-46/47).
package application

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
)

// Store es la persistencia del buscador de normativa.
type Store interface {
	// PlatformRole devuelve el rol de plataforma de una persona ACTIVE ("" si no tiene); ErrDenied si no está activa.
	PlatformRole(ctx context.Context, user string) (string, error)
	Documents(ctx context.Context, status string) ([]domain.Document, error)
	ActiveCount(ctx context.Context) (int, error)
	Document(ctx context.Context, id string) (domain.Document, error)
	// CreateDocument y UpdateDocument guardan la norma y reemplazan sus fragmentos en una transacción.
	CreateDocument(ctx context.Context, user, id string, in domain.DocumentInput, fragments []string) (domain.Document, error)
	UpdateDocument(ctx context.Context, user, id string, in domain.DocumentInput, fragments []string) (domain.Document, error)
	SetStatus(ctx context.Context, user, id, status string) (domain.Document, error)
	// Search busca en los fragmentos de la normativa activa, del más al menos pertinente.
	Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error)
}

// Service orquesta permisos y validación.
type Service struct {
	store Store
	newID func() string
}

func New(store Store, newID func() string) *Service { return &Service{store: store, newID: newID} }

// Status devuelve cuántas normas activas hay para buscar.
func (s *Service) Status(ctx context.Context, user string) (int, error) {
	if _, err := s.store.PlatformRole(ctx, user); err != nil {
		return 0, err
	}
	return s.store.ActiveCount(ctx)
}

// Search busca en la normativa activa; cualquier persona activa puede hacerlo.
func (s *Service) Search(ctx context.Context, user, query string) ([]domain.SearchResult, error) {
	if _, err := s.store.PlatformRole(ctx, user); err != nil {
		return nil, err
	}
	q, err := domain.NormalizeQuery(query)
	if err != nil {
		return nil, err
	}
	return s.store.Search(ctx, q, domain.MaxResults)
}

func (s *Service) requireAdmin(ctx context.Context, user string) error {
	role, err := s.store.PlatformRole(ctx, user)
	if err != nil {
		return err
	}
	if role != "SUPER_ADMIN" {
		return domain.ErrDenied
	}
	return nil
}

// Documents lista la normativa (solo administrador general).
func (s *Service) Documents(ctx context.Context, user, status string) ([]domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return nil, err
	}
	if status != domain.StatusActive && status != domain.StatusArchived {
		return nil, &domain.ValidationError{Fields: map[string]string{"status": "Usa ACTIVE o ARCHIVED."}}
	}
	return s.store.Documents(ctx, status)
}

// Document devuelve un documento con su texto (solo administrador general).
func (s *Service) Document(ctx context.Context, user, id string) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	if !domain.ValidID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	return s.store.Document(ctx, id)
}

// Create carga una norma nueva, activa y lista para buscar.
func (s *Service) Create(ctx context.Context, user string, in domain.DocumentInput) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	doc, err := domain.NormalizeDocument(in)
	if err != nil {
		return domain.Document{}, err
	}
	return s.store.CreateDocument(ctx, user, s.newID(), doc, domain.Fragments(doc.Content))
}

// Update reemplaza título, referencia y texto, y vuelve a indexar la norma.
func (s *Service) Update(ctx context.Context, user, id string, in domain.DocumentInput) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	if !domain.ValidID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	doc, err := domain.NormalizeDocument(in)
	if err != nil {
		return domain.Document{}, err
	}
	return s.store.UpdateDocument(ctx, user, id, doc, domain.Fragments(doc.Content))
}

// SetStatus archiva (deja de aparecer en búsquedas) o reactiva una norma.
func (s *Service) SetStatus(ctx context.Context, user, id, status string) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	if !domain.ValidID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	return s.store.SetStatus(ctx, user, id, status)
}
