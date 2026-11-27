package application

import (
	"context"
	"errors"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/platformsettings/domain"
)

type storeStub struct {
	role    string
	roleErr error
	contact domain.SupportContact
	saved   int
}

func (s *storeStub) PlatformRole(context.Context, string) (string, error) { return s.role, s.roleErr }
func (s *storeStub) SupportContact(context.Context) (domain.SupportContact, error) {
	return s.contact, nil
}
func (s *storeStub) SaveSupportContact(_ context.Context, _ string, c domain.SupportContact) (domain.SupportContact, error) {
	s.saved++
	s.contact = c
	return c, nil
}

func TestAnyActiveUserReadsSupportContact(t *testing.T) {
	store := &storeStub{role: "USER", contact: domain.SupportContact{Email: domain.DefaultSupportEmail}}
	got, err := New(store).SupportContact(context.Background(), "uid")
	if err != nil || got.Email != domain.DefaultSupportEmail {
		t.Fatalf("unexpected %+v %v", got, err)
	}
	store.roleErr = domain.ErrDenied
	if _, err := New(store).SupportContact(context.Background(), "uid"); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("inactive user must be denied, got %v", err)
	}
}

func TestOnlySuperAdminUpdatesSupportContact(t *testing.T) {
	store := &storeStub{role: "USER"}
	if _, err := New(store).UpdateSupportContact(context.Background(), "uid", "a@b.pe", ""); !errors.Is(err, domain.ErrDenied) || store.saved != 0 {
		t.Fatalf("expected denied, got %v", err)
	}
	store.role = "SUPER_ADMIN"
	var validation *domain.ValidationError
	if _, err := New(store).UpdateSupportContact(context.Background(), "uid", "mal", ""); !errors.As(err, &validation) || store.saved != 0 {
		t.Fatalf("expected validation, got %v", err)
	}
	got, err := New(store).UpdateSupportContact(context.Background(), "uid", "soporte@bpm.pe", "+51 987 654 321")
	if err != nil || got.Email != "soporte@bpm.pe" || got.WhatsApp != "51987654321" || store.saved != 1 {
		t.Fatalf("unexpected %+v %v", got, err)
	}
}
