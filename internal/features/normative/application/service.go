// Package application implementa los casos de uso del agente normativo (RF-46/47).
package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
)

// Store es la persistencia del agente normativo.
type Store interface {
	// PlatformRole devuelve el rol de plataforma de una persona ACTIVE ("" si no tiene); ErrDenied si no está activa.
	PlatformRole(ctx context.Context, user string) (string, error)
	Documents(ctx context.Context, status string) ([]domain.Document, error)
	ActiveDocuments(ctx context.Context) ([]domain.Document, error)
	// ActiveCharacters suma los caracteres de la normativa activa sin contar [excludeID].
	ActiveCharacters(ctx context.Context, excludeID string) (int, error)
	Document(ctx context.Context, id string) (domain.Document, error)
	CreateDocument(ctx context.Context, user, id string, in domain.DocumentInput) (domain.Document, error)
	UpdateDocument(ctx context.Context, user, id string, in domain.DocumentInput) (domain.Document, error)
	SetStatus(ctx context.Context, user, id, status string) (domain.Document, error)
	QuestionsSince(ctx context.Context, user string, since time.Time) (int, error)
	LogQuestion(ctx context.Context, user, question string, answer domain.Answer) error
}

// Answerer consulta al modelo con la normativa como única fuente.
type Answerer interface {
	Answer(ctx context.Context, documents []domain.Document, question string) (domain.Answer, error)
}

// Service orquesta permisos, límites y registro.
type Service struct {
	store    Store
	answerer Answerer
	now      func() time.Time
	newID    func() string
}

// New crea el servicio. Con [answerer] nulo el agente queda apagado (sin clave de API).
func New(store Store, answerer Answerer, newID func() string) *Service {
	return &Service{store: store, answerer: answerer, now: time.Now, newID: newID}
}

// Status informa si el agente está disponible y cuántos documentos activos hay.
type Status struct {
	Enabled   bool
	Documents int
}

func (s *Service) Status(ctx context.Context, user string) (Status, error) {
	if _, err := s.store.PlatformRole(ctx, user); err != nil {
		return Status{}, err
	}
	docs, err := s.store.ActiveDocuments(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{Enabled: s.answerer != nil, Documents: len(docs)}, nil
}

// Ask responde una pregunta con la normativa activa y la registra.
func (s *Service) Ask(ctx context.Context, user, question string) (domain.Answer, error) {
	if _, err := s.store.PlatformRole(ctx, user); err != nil {
		return domain.Answer{}, err
	}
	q, err := domain.NormalizeQuestion(question)
	if err != nil {
		return domain.Answer{}, err
	}
	if s.answerer == nil {
		return domain.Answer{}, domain.ErrDisabled
	}
	count, err := s.store.QuestionsSince(ctx, user, s.now().Add(-time.Hour))
	if err != nil {
		return domain.Answer{}, err
	}
	if count >= domain.QuestionsPerHour {
		return domain.Answer{}, domain.ErrRateLimited
	}
	docs, err := s.store.ActiveDocuments(ctx)
	if err != nil {
		return domain.Answer{}, err
	}
	if len(docs) == 0 {
		return domain.Answer{}, domain.ErrNoDocuments
	}
	answer, err := s.answerer.Answer(ctx, docs, q)
	if err != nil {
		return domain.Answer{}, err
	}
	if err = s.store.LogQuestion(ctx, user, q, answer); err != nil {
		return domain.Answer{}, err
	}
	return answer, nil
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

// Document devuelve un documento con su contenido (solo administrador general).
func (s *Service) Document(ctx context.Context, user, id string) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	if !domain.ValidID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	return s.store.Document(ctx, id)
}

// Create carga una norma nueva, activa.
func (s *Service) Create(ctx context.Context, user string, in domain.DocumentInput) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	doc, err := domain.NormalizeDocument(in)
	if err != nil {
		return domain.Document{}, err
	}
	if err = s.checkSize(ctx, "", len([]rune(doc.Content))); err != nil {
		return domain.Document{}, err
	}
	return s.store.CreateDocument(ctx, user, s.newID(), doc)
}

// Update reemplaza título, referencia y texto de una norma.
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
	current, err := s.store.Document(ctx, id)
	if err != nil {
		return domain.Document{}, err
	}
	if current.Status == domain.StatusActive {
		if err = s.checkSize(ctx, id, len([]rune(doc.Content))); err != nil {
			return domain.Document{}, err
		}
	}
	return s.store.UpdateDocument(ctx, user, id, doc)
}

// SetStatus archiva (deja de usarse en respuestas) o reactiva una norma.
func (s *Service) SetStatus(ctx context.Context, user, id, status string) (domain.Document, error) {
	if err := s.requireAdmin(ctx, user); err != nil {
		return domain.Document{}, err
	}
	if !domain.ValidID(id) {
		return domain.Document{}, domain.ErrNotFound
	}
	if status == domain.StatusActive {
		current, err := s.store.Document(ctx, id)
		if err != nil {
			return domain.Document{}, err
		}
		if err = s.checkSize(ctx, id, current.Characters); err != nil {
			return domain.Document{}, err
		}
	}
	return s.store.SetStatus(ctx, user, id, status)
}

func (s *Service) checkSize(ctx context.Context, excludeID string, adding int) error {
	total, err := s.store.ActiveCharacters(ctx, excludeID)
	if err != nil {
		return err
	}
	if total+adding > domain.MaxActiveCharacters {
		return domain.ErrTooLarge
	}
	return nil
}
