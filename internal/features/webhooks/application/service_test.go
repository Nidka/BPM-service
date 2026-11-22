package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
)

type store struct {
	admin    bool
	items    []domain.Endpoint
	recorded []int
}

func (s *store) CanManage(context.Context, string, string) (bool, error) { return s.admin, nil }
func (s *store) List(context.Context, string) ([]domain.Endpoint, error) { return s.items, nil }
func (s *store) Get(context.Context, string, string) (domain.Endpoint, error) {
	if len(s.items) == 0 {
		return domain.Endpoint{}, domain.ErrNotFound
	}
	return s.items[0], nil
}
func (s *store) Create(_ context.Context, _ string, e domain.Endpoint) (domain.Endpoint, error) {
	return e, nil
}
func (s *store) Update(context.Context, string, string, string, domain.Input) (domain.Endpoint, error) {
	return domain.Endpoint{}, nil
}
func (s *store) SetSecret(_ context.Context, _, _, _, secret string) (domain.Endpoint, error) {
	return domain.Endpoint{Secret: secret}, nil
}
func (s *store) Delete(context.Context, string, string, string) error { return nil }
func (s *store) Subscribed(context.Context, string, string) ([]domain.Endpoint, error) {
	return s.items, nil
}
func (s *store) RecordResult(_ context.Context, _ string, status int, _ string, _ time.Time) error {
	s.recorded = append(s.recorded, status)
	return nil
}

type sender struct{ status []int }

func (s *sender) Send(_ context.Context, e domain.Endpoint, _ domain.Delivery) (int, error) {
	code := s.status[0]
	s.status = s.status[1:]
	if code >= 300 {
		return code, errors.New("fallo")
	}
	return code, nil
}

func TestOnlyAdminsManageAndSecretIsGenerated(t *testing.T) {
	st := &store{}
	svc := New(st, &sender{}, func() string { return "11111111-1111-4111-8111-111111111111" })
	in := domain.Input{URL: "https://n8n.miempresa.pe/w", EventTypes: []string{"CHECKLIST_MISSING"}, Active: true}
	if _, err := svc.Create(context.Background(), "u", "org", in); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("un no administrador no gestiona webhooks: %v", err)
	}
	st.admin = true
	e, err := svc.Create(context.Background(), "u", "org", in)
	if err != nil || len(e.Secret) < 40 {
		t.Fatalf("crear: %+v %v", e, err)
	}
	st.items = make([]domain.Endpoint, domain.MaxEndpoints)
	if _, err = svc.Create(context.Background(), "u", "org", in); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("límite: %v", err)
	}
	rotated, _ := svc.RotateSecret(context.Background(), "u", "org", "id")
	if rotated.Secret == e.Secret {
		t.Fatal("rotar debe cambiar el secreto")
	}
}

func TestDeliverRetriesWhenAnyEndpointFails(t *testing.T) {
	st := &store{items: []domain.Endpoint{
		{ID: "a", Active: true, EventTypes: []string{"TEMPERATURE_DANGER"}},
		{ID: "b", Active: true, EventTypes: []string{"TEMPERATURE_DANGER"}},
		{ID: "c", Active: true, EventTypes: []string{"CHECKLIST_MISSING"}},
	}}
	svc := New(st, &sender{status: []int{200, 500}}, func() string { return "x" })
	err := svc.Deliver(context.Background(), domain.Delivery{Type: "TEMPERATURE_DANGER", OrganizationID: "org"})
	if !errors.Is(err, ErrDeliveryFailed) || len(st.recorded) != 2 {
		t.Fatalf("debía reintentar y registrar 2 entregas: %v %v", err, st.recorded)
	}
}
