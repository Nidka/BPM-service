package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"testing"
	"time"
)

type deliveryRepository struct {
	row      domain.Delivery
	eligible bool
	out      domain.Outcome
	staged   int
}

func (r *deliveryRepository) Stage(context.Context, string, string, string, time.Time) error {
	r.staged++
	return nil
}
func (r *deliveryRepository) Claim(context.Context, []string, time.Time) (*domain.Delivery, error) {
	if r.row.ID == "" {
		return nil, nil
	}
	v := r.row
	r.row.ID = ""
	return &v, nil
}
func (r *deliveryRepository) Eligible(context.Context, domain.Delivery) (bool, error) {
	return r.eligible, nil
}
func (r *deliveryRepository) Finish(_ context.Context, _ domain.Delivery, v domain.Outcome, _ time.Time) error {
	r.out = v
	return nil
}

type sender struct{ calls int }

func (s *sender) Send(context.Context, string, domain.Message) (string, error) {
	s.calls++
	return "accepted", nil
}
func TestDispatcherRechecksRecipientBeforeProvider(t *testing.T) {
	r := &deliveryRepository{row: domain.Delivery{ID: "d", Channel: "EMAIL"}, eligible: false}
	p := &sender{}
	s := NewDispatcher(r, map[string]Provider{"EMAIL": p}, "from@example.invalid")
	if e := s.SendDue(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	if p.calls != 0 || r.out.Status != "CANCELLED" {
		t.Fatal("revoked recipient sent")
	}
}
func TestDispatcherAcceptanceDoesNotMeanReadOrDelivered(t *testing.T) {
	r := &deliveryRepository{row: domain.Delivery{ID: "d", Channel: "EMAIL"}, eligible: true}
	p := &sender{}
	s := NewDispatcher(r, map[string]Provider{"EMAIL": p}, "from@example.invalid")
	if e := s.SendDue(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	if p.calls != 1 || r.out.Status != "SENT" || r.out.ProviderID != "accepted" {
		t.Fatal("incorrect provider outcome")
	}
}

func (r *deliveryRepository) Reconcile(context.Context, time.Time) error { return nil }
