package application

import (
	"context"
	"errors"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
)

// seedStore solo implementa lo que usa CreateOrganization; el resto entra por la interfaz embebida (nil).
type seedStore struct {
	Store
	role    string
	created []string
}

func (s *seedStore) Actor(context.Context, string) (domain.Actor, error) {
	return domain.Actor{ID: "uid", Role: s.role}, nil
}

func (s *seedStore) CreateOrganization(_ context.Context, _ domain.Actor, o domain.Organization) error {
	s.created = append(s.created, o.ID)
	return nil
}

type seedWork struct{ store *seedStore }

func (w seedWork) Within(_ context.Context, fn func(Store) error) error { return fn(w.store) }

type recordingSeeder struct {
	calls [][2]string
	err   error
}

func (r *recordingSeeder) SeedOrganization(_ context.Context, org, actor string) error {
	r.calls = append(r.calls, [2]string{org, actor})
	return r.err
}

func newOrg() domain.Organization {
	return domain.Organization{ID: "10000000-0000-0000-0000-000000000001", LegalName: "Restaurante Sol S.A.C.", BusinessType: "RESTAURANT"}
}

func TestOrganizacionNuevaSePreparaConElContenidoBase(t *testing.T) {
	store, seeder := &seedStore{role: "SUPER_ADMIN"}, &recordingSeeder{}
	service := New(seedWork{store}, WithSeeder(seeder))
	if _, err := service.CreateOrganization(context.Background(), "uid", newOrg()); err != nil {
		t.Fatal(err)
	}
	if len(seeder.calls) != 1 || seeder.calls[0] != [2]string{newOrg().ID, "uid"} {
		t.Fatalf("el preparador debe recibir la organización y el actor: %v", seeder.calls)
	}
	// Repetir la solicitud (idempotente) vuelve a invocarlo, de modo que completa una instalación interrumpida.
	if _, err := service.CreateOrganization(context.Background(), "uid", newOrg()); err != nil || len(seeder.calls) != 2 {
		t.Fatal("la repetición también prepara la organización", err, len(seeder.calls))
	}
}

func TestSinPermisoONoSePreparaNada(t *testing.T) {
	seeder := &recordingSeeder{}
	service := New(seedWork{&seedStore{role: "ADMIN"}}, WithSeeder(seeder))
	if _, err := service.CreateOrganization(context.Background(), "uid", newOrg()); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("solo SUPER_ADMIN crea organizaciones", err)
	}
	if len(seeder.calls) != 0 {
		t.Fatal("no se prepara una organización que no se creó")
	}
}

func TestFalloDelPreparadorSeInformaParaReintentar(t *testing.T) {
	boom := errors.New("catálogo no disponible")
	service := New(seedWork{&seedStore{role: "SUPER_ADMIN"}}, WithSeeder(&recordingSeeder{err: boom}))
	if _, err := service.CreateOrganization(context.Background(), "uid", newOrg()); !errors.Is(err, boom) {
		t.Fatal("el fallo del contenido base debe llegar al cliente para reintentar", err)
	}
}

func TestSinPreparadorLaCreacionNoCambia(t *testing.T) {
	if _, err := New(seedWork{&seedStore{role: "SUPER_ADMIN"}}).CreateOrganization(context.Background(), "uid", newOrg()); err != nil {
		t.Fatal(err)
	}
}
