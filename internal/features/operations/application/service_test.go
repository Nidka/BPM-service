package application

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"testing"
)

type fakeWork struct {
	store *fakeStore
	calls int
}

func (w *fakeWork) Within(ctx context.Context, fn func(Store) error) error {
	w.calls++
	return fn(w.store)
}

type fakeStore struct {
	Store
	role    string
	created bool
}

func (s *fakeStore) Actor(context.Context, string, string) (domain.Actor, error) {
	return domain.Actor{ID: "actor", Role: s.role}, nil
}
func TestInvalidInputDoesNotOpenTransaction(t *testing.T) {
	w := &fakeWork{}
	_, err := New(w).CreateSupplier(context.Background(), "actor", "org", "stable-key", domain.Supplier{Name: "x"})
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || w.calls != 0 {
		t.Fatal(err, w.calls)
	}
}
func TestWorkerCannotConfigureOrReceive(t *testing.T) {
	w := &fakeWork{store: &fakeStore{role: "WORKER"}}
	_, err := New(w).CreateSupplier(context.Background(), "actor", "org", "stable-key", domain.Supplier{ID: "10000000-0000-0000-0000-000000000001", Name: "Proveedor de prueba"})
	if !errors.Is(err, domain.ErrDenied) || w.store.created {
		t.Fatal("permiso incorrecto", err)
	}
}
