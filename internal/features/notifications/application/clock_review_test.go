package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"testing"
	"time"
)

type deliveryClockReview struct {
	claims    []time.Time
	sentAfter time.Time
	finishes  int
}

func (r *deliveryClockReview) Stage(context.Context, string, string, string, time.Time) error {
	return nil
}
func (r *deliveryClockReview) Claim(_ context.Context, _ []string, now time.Time) (*domain.Delivery, error) {
	r.claims = append(r.claims, now)
	if len(r.claims) > 2 {
		return nil, nil
	}
	return &domain.Delivery{ID: "fixture-delivery", Channel: "EMAIL"}, nil
}
func (r *deliveryClockReview) Eligible(context.Context, domain.Delivery) (bool, error) {
	return true, nil
}
func (r *deliveryClockReview) Finish(context.Context, domain.Delivery, domain.Outcome, time.Time) error {
	r.finishes++
	return nil
}
func (r *deliveryClockReview) Send(context.Context, string, domain.Message) (string, error) {
	if r.sentAfter.IsZero() {
		r.sentAfter = time.Now()
	}
	return "accepted", nil
}
func TestDispatcherClaimsFreshLeaseAfterEachSend(t *testing.T) {
	r := &deliveryClockReview{}
	dispatcher := NewDispatcher(r, map[string]Provider{"EMAIL": r}, "sender@example.invalid")
	if err := dispatcher.SendDue(context.Background(), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(r.claims) < 2 || r.claims[1].Before(r.sentAfter) {
		t.Fatal("second delivery lease uses tick timestamp from before first provider call")
	}
}

func (r *deliveryClockReview) Reconcile(context.Context, time.Time) error { return nil }
