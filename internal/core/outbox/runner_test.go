package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryQueue struct {
	item                        Claimed
	completed, failed, deferred int
	retry                       time.Time
}

func (q *memoryQueue) Claim(context.Context, []string, time.Time, int, time.Duration) ([]Claimed, error) {
	if q.item.Type == "" {
		return nil, nil
	}
	v := q.item
	q.item.Type = ""
	return []Claimed{v}, nil
}
func (q *memoryQueue) Complete(context.Context, Claimed, time.Time) error { q.completed++; return nil }
func (q *memoryQueue) Retry(_ context.Context, _ Claimed, at time.Time, _ string, _ bool) error {
	q.failed++
	q.retry = at
	return nil
}
func (q *memoryQueue) Defer(context.Context, Claimed, time.Time) error { q.deferred++; return nil }
func TestRunnerRoutesAndDefersMissingProviderWithoutFailing(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	q := &memoryQueue{item: Claimed{Event: Event{Type: "known", CreatedAt: now}, Attempts: 1}}
	r := NewRunner(q, map[string]Handler{"known": func(context.Context, Event) error { return ErrUnavailable }}, Options{Clock: func() time.Time { return now }, Jitter: func(time.Duration) time.Duration { return 0 }})
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.deferred != 1 || q.completed != 0 || q.failed != 0 {
		t.Fatal("provider unavailable consumed failure budget")
	}
}
func TestRunnerRetriesAndCompletesOnlySuccessfulHandlers(t *testing.T) {
	now := time.Now().UTC()
	q := &memoryQueue{item: Claimed{Event: Event{Type: "known", CreatedAt: now}, Attempts: 3}}
	r := NewRunner(q, map[string]Handler{"known": func(context.Context, Event) error { return errors.New("sensitive provider body") }}, Options{Clock: func() time.Time { return now }, Jitter: func(time.Duration) time.Duration { return 0 }})
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.failed != 1 || q.completed != 0 || !q.retry.After(now) {
		t.Fatal("failed handler not retried")
	}
	q.item.Type = "known"
	r.handlers["known"] = func(context.Context, Event) error { return nil }
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q.completed != 1 {
		t.Fatal("successful handler not completed")
	}
}
