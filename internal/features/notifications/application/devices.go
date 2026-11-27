package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"strings"
)

type DeviceStore interface {
	Authorize(context.Context, string, string) error
	Register(context.Context, string, string, domain.DeviceInput) (domain.Device, error)
	Deactivate(context.Context, string, string, string) (domain.Device, error)
	// TestPush crea un aviso de prueba solo para la persona (sin sede), con su evento de envío. Máximo uno por minuto.
	TestPush(context.Context, string, string) error
}
type DevicesRepository interface {
	Within(context.Context, func(DeviceStore) error) error
}
type Devices struct{ repository DevicesRepository }

func NewDevices(r DevicesRepository) *Devices { return &Devices{r} }
func (s *Devices) Register(ctx context.Context, user, org string, v domain.DeviceInput) (out domain.Device, err error) {
	if !domain.ValidUID(user) || !domain.ValidOrganization(org) {
		return out, domain.ErrDenied
	}
	if err = v.Validate(); err != nil {
		return
	}
	v.ID = strings.ToLower(v.ID)
	v.InstallationID = strings.ToLower(v.InstallationID)
	err = s.repository.Within(ctx, func(tx DeviceStore) error {
		if e := tx.Authorize(ctx, user, org); e != nil {
			return e
		}
		var e error
		out, e = tx.Register(ctx, user, org, v)
		return e
	})
	return
}

// TestPush envía una notificación de prueba al propio celular de la persona.
func (s *Devices) TestPush(ctx context.Context, user, org string) error {
	if !domain.ValidUID(user) || !domain.ValidOrganization(org) {
		return domain.ErrDenied
	}
	return s.repository.Within(ctx, func(tx DeviceStore) error {
		if e := tx.Authorize(ctx, user, org); e != nil {
			return e
		}
		return tx.TestPush(ctx, user, org)
	})
}

func (s *Devices) Deactivate(ctx context.Context, user, org, id string) (out domain.Device, err error) {
	if !domain.ValidUID(user) || !domain.ValidOrganization(org) {
		return out, domain.ErrDenied
	}
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	id = strings.ToLower(id)
	err = s.repository.Within(ctx, func(tx DeviceStore) error {
		if e := tx.Authorize(ctx, user, org); e != nil {
			return e
		}
		var e error
		out, e = tx.Deactivate(ctx, user, org, id)
		return e
	})
	return
}
