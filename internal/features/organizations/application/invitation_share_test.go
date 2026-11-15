package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
)

// shareStore es un almacén mínimo para probar la emisión de invitaciones.
type shareStore struct {
	Store
	queued []string
}

func (s *shareStore) Actor(context.Context, string) (domain.Actor, error) {
	return domain.Actor{ID: "admin"}, nil
}
func (s *shareStore) LockForManagement(context.Context, domain.Actor, string) (string, error) {
	return "ADMIN", nil
}
func (s *shareStore) CheckLocations(context.Context, string, []string) error { return nil }
func (s *shareStore) CreateInvitation(_ context.Context, _ domain.Actor, _ string, v domain.Invitation) (domain.Invitation, bool, error) {
	v.Status = domain.InvitationPending
	return v, v.ID != replayID, nil
}
func (s *shareStore) QueueInvitationEmail(_ context.Context, _ domain.Actor, _ domain.Invitation, token string) error {
	s.queued = append(s.queued, token)
	return nil
}
func (s *shareStore) LockInvitation(_ context.Context, _, id string) (domain.Invitation, error) {
	return domain.Invitation{ID: id, Status: domain.InvitationPending, LastSentAt: time.Now().Add(-time.Hour)}, nil
}
func (s *shareStore) ResendInvitation(_ context.Context, _ domain.Actor, _, id, _ string) (domain.Invitation, error) {
	return domain.Invitation{ID: id, Status: domain.InvitationPending}, nil
}

type shareWork struct{ store *shareStore }

func (w shareWork) Within(_ context.Context, fn func(Store) error) error { return fn(w.store) }

const (
	inviteID = "30000000-0000-0000-0000-000000000001"
	replayID = "30000000-0000-0000-0000-0000000000ff"
)

func TestInvitationShareWithAcceptURL(t *testing.T) {
	st := &shareStore{}
	s := New(shareWork{st}, WithInvitationDelivery("https://appbpm.com/cuenta/invitacion", true))
	in := InvitationInput{ID: inviteID, Email: "Nuevo@Example.com", Role: "ADMIN"}
	_, share, created, err := s.InviteShared(context.Background(), "admin", "o", in)
	if err != nil || !created || !share.Issued || share.Token != "" || !strings.HasPrefix(share.AcceptURL, "https://appbpm.com/cuenta/invitacion?token=") {
		t.Fatal(share, created, err)
	}
	if len(st.queued) != 1 || !strings.HasSuffix(share.AcceptURL, st.queued[0]) {
		t.Fatal("el correo debe llevar el mismo token", st.queued)
	}
	_, share, err = s.ResendShared(context.Background(), "admin", "o", inviteID)
	if err != nil || !strings.Contains(share.AcceptURL, "?token=") || len(st.queued) != 2 {
		t.Fatal(share, err, st.queued)
	}
	// Repetición idempotente: no existe token que compartir.
	in.ID = replayID
	_, share, created, err = s.InviteShared(context.Background(), "admin", "o", in)
	if err != nil || created || share.Issued || share.AcceptURL != "" || share.Token != "" {
		t.Fatal(share, created, err)
	}
}

func TestInvitationShareWithoutURLReturnsToken(t *testing.T) {
	st := &shareStore{}
	s := New(shareWork{st}, WithInvitationDelivery("", true))
	_, share, _, err := s.InviteShared(context.Background(), "admin", "o", InvitationInput{ID: inviteID, Email: "a@example.com", Role: "ADMIN"})
	if err != nil || share.AcceptURL != "" || len(share.Token) != 43 {
		t.Fatal(share, err)
	}
}

func TestInvitationWithoutMailerIsCreatedAndNotQueued(t *testing.T) {
	st := &shareStore{}
	s := New(shareWork{st}, WithInvitationDelivery("https://appbpm.com/cuenta/invitacion", false))
	_, share, created, err := s.InviteShared(context.Background(), "admin", "o", InvitationInput{ID: inviteID, Email: "a@example.com", Role: "CHEF", LocationIDs: []string{"40000000-0000-0000-0000-000000000001"}})
	if err != nil || !created || share.AcceptURL == "" || len(st.queued) != 0 {
		t.Fatal(share, created, err, st.queued)
	}
	if _, _, err = s.ResendShared(context.Background(), "admin", "o", inviteID); err != nil || len(st.queued) != 0 {
		t.Fatal(err, st.queued)
	}
}
