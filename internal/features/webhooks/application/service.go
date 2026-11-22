// Package application implementa la gestión y la entrega de webhooks (RF-48).
package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
)

// Store es la persistencia de los webhooks.
type Store interface {
	// CanManage indica si la persona es ADMIN activa de la empresa o SUPER_ADMIN.
	CanManage(ctx context.Context, user, org string) (bool, error)
	List(ctx context.Context, org string) ([]domain.Endpoint, error)
	Get(ctx context.Context, org, id string) (domain.Endpoint, error)
	Create(ctx context.Context, user string, e domain.Endpoint) (domain.Endpoint, error)
	Update(ctx context.Context, user, org, id string, in domain.Input) (domain.Endpoint, error)
	SetSecret(ctx context.Context, user, org, id, secret string) (domain.Endpoint, error)
	Delete(ctx context.Context, user, org, id string) error
	// Subscribed devuelve los webhooks activos de la empresa suscritos a [eventType].
	Subscribed(ctx context.Context, org, eventType string) ([]domain.Endpoint, error)
	RecordResult(ctx context.Context, id string, status int, failure string, at time.Time) error
}

// Sender entrega un evento firmado y devuelve el estado HTTP.
type Sender interface {
	Send(ctx context.Context, endpoint domain.Endpoint, delivery domain.Delivery) (int, error)
}

// Service orquesta permisos, validación y entrega.
type Service struct {
	store  Store
	sender Sender
	newID  func() string
	now    func() time.Time
}

func New(store Store, sender Sender, newID func() string) *Service {
	return &Service{store: store, sender: sender, newID: newID, now: time.Now}
}

func (s *Service) authorize(ctx context.Context, user, org string) error {
	ok, err := s.store.CanManage(ctx, user, org)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrDenied
	}
	return nil
}

func (s *Service) List(ctx context.Context, user, org string) ([]domain.Endpoint, error) {
	if err := s.authorize(ctx, user, org); err != nil {
		return nil, err
	}
	return s.store.List(ctx, org)
}

// Create registra un webhook con un secreto nuevo, que solo se devuelve en esta respuesta.
func (s *Service) Create(ctx context.Context, user, org string, in domain.Input) (domain.Endpoint, error) {
	if err := s.authorize(ctx, user, org); err != nil {
		return domain.Endpoint{}, err
	}
	in, err := domain.Normalize(in)
	if err != nil {
		return domain.Endpoint{}, err
	}
	current, err := s.store.List(ctx, org)
	if err != nil {
		return domain.Endpoint{}, err
	}
	if len(current) >= domain.MaxEndpoints {
		return domain.Endpoint{}, domain.ErrLimit
	}
	secret, err := NewSecret()
	if err != nil {
		return domain.Endpoint{}, err
	}
	return s.store.Create(ctx, user, domain.Endpoint{ID: s.newID(), OrganizationID: org, URL: in.URL, Secret: secret, EventTypes: in.EventTypes, Active: in.Active})
}

func (s *Service) Update(ctx context.Context, user, org, id string, in domain.Input) (domain.Endpoint, error) {
	if err := s.authorize(ctx, user, org); err != nil {
		return domain.Endpoint{}, err
	}
	in, err := domain.Normalize(in)
	if err != nil {
		return domain.Endpoint{}, err
	}
	return s.store.Update(ctx, user, org, id, in)
}

// RotateSecret reemplaza el secreto; el anterior deja de ser válido de inmediato.
func (s *Service) RotateSecret(ctx context.Context, user, org, id string) (domain.Endpoint, error) {
	if err := s.authorize(ctx, user, org); err != nil {
		return domain.Endpoint{}, err
	}
	secret, err := NewSecret()
	if err != nil {
		return domain.Endpoint{}, err
	}
	return s.store.SetSecret(ctx, user, org, id, secret)
}

func (s *Service) Delete(ctx context.Context, user, org, id string) error {
	if err := s.authorize(ctx, user, org); err != nil {
		return err
	}
	return s.store.Delete(ctx, user, org, id)
}

// Test envía un evento de prueba y devuelve el estado HTTP que respondió el destino.
func (s *Service) Test(ctx context.Context, user, org, id string) (int, error) {
	if err := s.authorize(ctx, user, org); err != nil {
		return 0, err
	}
	endpoint, err := s.store.Get(ctx, org, id)
	if err != nil {
		return 0, err
	}
	now := s.now().UTC()
	status, err := s.sender.Send(ctx, endpoint, domain.Delivery{ID: s.newID(), Type: domain.EventTest, OrganizationID: org,
		Title: "Prueba de BPM", Message: "Si ves este mensaje, el webhook está bien configurado.", OccurredAt: now})
	failure := ""
	if err != nil {
		failure = "No se pudo entregar"
	}
	_ = s.store.RecordResult(ctx, id, status, failure, now)
	return status, err
}

// ErrDeliveryFailed indica que al menos un destino no aceptó el evento (el outbox reintenta).
var ErrDeliveryFailed = errors.New("webhooks: delivery failed")

// Deliver entrega [delivery] a cada webhook activo suscrito. Los destinos que ya lo recibieron pueden
// recibirlo de nuevo en un reintento: el encabezado X-BPM-Delivery permite descartar duplicados.
func (s *Service) Deliver(ctx context.Context, delivery domain.Delivery) error {
	endpoints, err := s.store.Subscribed(ctx, delivery.OrganizationID, delivery.Type)
	if err != nil {
		return err
	}
	failed := false
	for _, e := range endpoints {
		if !e.Active || !slices.Contains(e.EventTypes, delivery.Type) {
			continue
		}
		status, err := s.sender.Send(ctx, e, delivery)
		failure := ""
		if err != nil {
			failed, failure = true, "No se pudo entregar"
		}
		_ = s.store.RecordResult(ctx, e.ID, status, failure, s.now().UTC())
	}
	if failed {
		return ErrDeliveryFailed
	}
	return nil
}

// NewSecret genera 32 bytes aleatorios en base64 URL (43 caracteres).
func NewSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}
