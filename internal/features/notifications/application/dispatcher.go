package application

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"time"
)

// Provider recibe una clave estable y un destino ya persistido; no el recurso sanitario.
type Provider interface {
	Send(context.Context, string, domain.Message) (string, error)
}

var (
	ErrUnavailable  = errors.New("proveedor sin configurar")
	ErrInvalidToken = errors.New("dispositivo no registrado")
	ErrPermanent    = errors.New("proveedor rechazó la entrega")
)

type DeliveryRepository interface {
	Reconcile(context.Context, time.Time) error
	Stage(context.Context, string, string, string, time.Time) error
	Claim(context.Context, []string, time.Time) (*domain.Delivery, error)
	Eligible(context.Context, domain.Delivery) (bool, error)
	Finish(context.Context, domain.Delivery, domain.Outcome, time.Time) error
}
type Dispatcher struct {
	repository DeliveryRepository
	providers  map[string]Provider
	from       string
	clock      func() time.Time
}

func NewDispatcher(r DeliveryRepository, p map[string]Provider, from string, clocks ...func() time.Time) *Dispatcher {
	clock := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		clock = clocks[0]
	}
	return &Dispatcher{r, p, from, clock}
}
func (s *Dispatcher) Dispatch(ctx context.Context, org, id string, now time.Time) error {
	if !domain.ValidOrganization(org) || !domain.ValidID(id) {
		return domain.ErrNotFound
	}
	return s.repository.Stage(ctx, org, id, s.from, now)
}
func (s *Dispatcher) SendDue(ctx context.Context, _ time.Time) error {
	if s.providers["FCM"] != nil {
		if e := s.repository.Reconcile(ctx, s.clock().UTC()); e != nil {
			return e
		}
	}
	channels := []string{}
	for k, v := range s.providers {
		if v != nil {
			channels = append(channels, k)
		}
	}
	for range 25 {
		row, e := s.repository.Claim(ctx, channels, s.clock().UTC())
		if e != nil {
			return e
		}
		if row == nil {
			return nil
		}
		v := *row
		allowed, e := s.repository.Eligible(ctx, v)
		if e != nil {
			return e
		}
		out := domain.Outcome{Status: "CANCELLED", Code: "RECIPIENT_OR_SOURCE_UNAVAILABLE"}
		if allowed {
			call, cancel := context.WithTimeout(ctx, 15*time.Second)
			id, sendErr := s.providers[v.Channel].Send(call, "bpm-alert/"+v.ID, v.Message)
			cancel()
			switch {
			case sendErr == nil:
				out = domain.Outcome{Status: "SENT", ProviderID: id}
			case errors.Is(sendErr, ErrInvalidToken):
				out = domain.Outcome{Status: "CANCELLED", Code: "DEVICE_UNREGISTERED", InvalidToken: true}
			case errors.Is(sendErr, ErrPermanent):
				out = domain.Outcome{Status: "FAILED", Code: "PROVIDER_REJECTED"}
			default:
				out = domain.Outcome{Status: "PENDING", Code: "PROVIDER_UNCERTAIN"}
				if v.Attempts >= 8 {
					out.Status = "UNCERTAIN"
				}
			}
		}
		if e = s.repository.Finish(ctx, v, out, s.clock().UTC()); e != nil {
			return e
		}
	}
	return nil
}
