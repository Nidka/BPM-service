package application

import (
	"context"
	"errors"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
)

type fakeStore struct {
	role      string
	inactive  bool
	fragments []string
	query     string
}

func (f *fakeStore) PlatformRole(context.Context, string) (string, error) {
	if f.inactive {
		return "", domain.ErrDenied
	}
	return f.role, nil
}
func (f *fakeStore) Documents(context.Context, string) ([]domain.Document, error) { return nil, nil }
func (f *fakeStore) ActiveCount(context.Context) (int, error)                     { return 2, nil }
func (f *fakeStore) Document(context.Context, string) (domain.Document, error) {
	return domain.Document{}, nil
}
func (f *fakeStore) CreateDocument(_ context.Context, _, id string, in domain.DocumentInput, fragments []string) (domain.Document, error) {
	f.fragments = fragments
	return domain.Document{ID: id, Title: in.Title}, nil
}
func (f *fakeStore) UpdateDocument(_ context.Context, _, id string, in domain.DocumentInput, fragments []string) (domain.Document, error) {
	f.fragments = fragments
	return domain.Document{ID: id}, nil
}
func (f *fakeStore) SetStatus(_ context.Context, _, id, status string) (domain.Document, error) {
	return domain.Document{ID: id, Status: status}, nil
}
func (f *fakeStore) Search(_ context.Context, q string, limit int) ([]domain.SearchResult, error) {
	f.query = q
	return []domain.SearchResult{{DocumentTitle: "NTS 142", Text: "0 °C a 4 °C"}}, nil
}

const id = "11111111-1111-4111-8111-111111111111"

func TestAnyActivePersonSearchesWithNormalizedQuery(t *testing.T) {
	store := &fakeStore{}
	svc := New(store, func() string { return id })
	got, err := svc.Search(context.Background(), "u", "   temperatura    pollo ")
	if err != nil || len(got) != 1 || store.query != "temperatura pollo" {
		t.Fatalf("búsqueda: %v %v %q", got, err, store.query)
	}
	if _, err = svc.Search(context.Background(), "u", "a"); err == nil {
		t.Fatal("una letra no es una búsqueda")
	}
	store.inactive = true
	if _, err = svc.Search(context.Background(), "u", "pollo"); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("una cuenta inactiva no busca: %v", err)
	}
}

func TestOnlySuperAdminManagesDocumentsAndTheyAreFragmented(t *testing.T) {
	store := &fakeStore{}
	svc := New(store, func() string { return id })
	in := domain.DocumentInput{Title: "RM 822-2018/MINSA", Content: "Artículo 1. Uno.\n\nArtículo 2. Dos."}
	if _, err := svc.Create(context.Background(), "u", in); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("un usuario común no carga normativa: %v", err)
	}
	store.role = "SUPER_ADMIN"
	if doc, err := svc.Create(context.Background(), "u", in); err != nil || doc.ID != id || len(store.fragments) != 2 {
		t.Fatalf("crear: %v %v fragmentos=%d", doc, err, len(store.fragments))
	}
	if _, err := svc.SetStatus(context.Background(), "u", "no-es-uuid", domain.StatusArchived); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("id inválido: %v", err)
	}
}
