package outbox

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"time"
)

type Queue interface {
	Claim(context.Context, []string, time.Time, int, time.Duration) ([]Claimed, error)
	Complete(context.Context, Claimed, time.Time) error
	Retry(context.Context, Claimed, time.Time, string, bool) error
	Defer(context.Context, Claimed, time.Time) error
}
type Handler func(context.Context, Event) error
type Options struct {
	Clock          func() time.Time
	Jitter         func(time.Duration) time.Duration
	BatchSize      int
	Lease, Timeout time.Duration
}
type Runner struct {
	queue    Queue
	handlers map[string]Handler
	options  Options
}

func NewRunner(q Queue, h map[string]Handler, o Options) *Runner {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Jitter == nil {
		o.Jitter = func(d time.Duration) time.Duration { return time.Duration(rand.Int64N(int64(d) + 1)) }
	}
	if o.BatchSize < 1 || o.BatchSize > 100 {
		o.BatchSize = 25
	}
	if o.Lease <= 0 {
		o.Lease = 2 * time.Minute
	}
	if o.Timeout <= 0 || o.Timeout >= o.Lease {
		o.Timeout = o.Lease / 2
	}
	return &Runner{q, h, o}
}
func (r *Runner) Tick(ctx context.Context) error {
	types := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		types = append(types, k)
	}
	slices.Sort(types)
	// Cada evento obtiene su lease justo antes del trabajo; no caduca esperando un lote serial.
	for range r.options.BatchSize {
		now := r.options.Clock().UTC()
		rows, e := r.queue.Claim(ctx, types, now, 1, r.options.Lease)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			return nil
		}
		v := rows[0]
		call, cancel := context.WithTimeout(ctx, r.options.Timeout)
		e = r.handlers[v.Type](call, v.Event)
		cancel()
		at := r.options.Clock().UTC()
		switch {
		case e == nil:
			e = r.queue.Complete(ctx, v, at)
		case errors.Is(e, ErrUnavailable):
			e = r.queue.Defer(ctx, v, at.Add(time.Minute))
		default:
			delay := time.Second * time.Duration(1<<min(v.Attempts, 10))
			delay = min(delay, 15*time.Minute)
			delay += r.options.Jitter(delay / 4)
			e = r.queue.Retry(ctx, v, at.Add(delay), "HANDLER_FAILED", v.Attempts >= 8 || (!v.ExpiresAt.IsZero() && !at.Before(v.ExpiresAt)))
		}
		if e != nil {
			return e
		}
	}
	return nil
}
