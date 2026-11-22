package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
)

type fakeStore struct {
	role      string
	inactive  bool
	docs      []domain.Document
	questions int
	logged    int
	active    int
}

func (f *fakeStore) PlatformRole(context.Context, string) (string, error) {
	if f.inactive {
		return "", domain.ErrDenied
	}
	return f.role, nil
}
func (f *fakeStore) Documents(context.Context, string) ([]domain.Document, error) { return f.docs, nil }
func (f *fakeStore) ActiveDocuments(context.Context) ([]domain.Document, error)   { return f.docs, nil }
func (f *fakeStore) ActiveCharacters(context.Context, string) (int, error)        { return f.active, nil }
func (f *fakeStore) Document(context.Context, string) (domain.Document, error) {
	return domain.Document{Status: domain.StatusArchived, Characters: 10}, nil
}
func (f *fakeStore) CreateDocument(_ context.Context, _, id string, in domain.DocumentInput) (domain.Document, error) {
	return domain.Document{ID: id, Title: in.Title}, nil
}
func (f *fakeStore) UpdateDocument(_ context.Context, _, id string, in domain.DocumentInput) (domain.Document, error) {
	return domain.Document{ID: id, Title: in.Title}, nil
}
func (f *fakeStore) SetStatus(_ context.Context, _, id, status string) (domain.Document, error) {
	return domain.Document{ID: id, Status: status}, nil
}
func (f *fakeStore) QuestionsSince(context.Context, string, time.Time) (int, error) {
	return f.questions, nil
}
func (f *fakeStore) LogQuestion(context.Context, string, string, domain.Answer) error {
	f.logged++
	return nil
}

type fakeAnswerer struct{ called bool }

func (f *fakeAnswerer) Answer(_ context.Context, docs []domain.Document, q string) (domain.Answer, error) {
	f.called = true
	return domain.Answer{Text: "Entre 0 y 4 °C.", Citations: []domain.Citation{{DocumentID: docs[0].ID, CitedText: "0 a 4 °C"}}}, nil
}

const id = "11111111-1111-4111-8111-111111111111"

func TestAskRequiresKeyDocumentsAndRespectsHourlyLimit(t *testing.T) {
	store := &fakeStore{docs: []domain.Document{{ID: id, Title: "NTS 142"}}}
	if _, err := New(store, nil, func() string { return id }).Ask(context.Background(), "u", "¿Temperatura de refrigeración?"); !errors.Is(err, domain.ErrDisabled) {
		t.Fatalf("sin clave debe estar apagado: %v", err)
	}
	answerer := &fakeAnswerer{}
	svc := New(store, answerer, func() string { return id })
	got, err := svc.Ask(context.Background(), "u", "  ¿Temperatura de refrigeración?  ")
	if err != nil || got.Text == "" || store.logged != 1 {
		t.Fatalf("respuesta=%v err=%v registradas=%d", got, err, store.logged)
	}
	store.questions = domain.QuestionsPerHour
	if _, err = svc.Ask(context.Background(), "u", "Otra pregunta"); !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("debía limitar: %v", err)
	}
	store.questions, store.docs = 0, nil
	if _, err = svc.Ask(context.Background(), "u", "Otra pregunta"); !errors.Is(err, domain.ErrNoDocuments) {
		t.Fatalf("sin documentos: %v", err)
	}
	if _, err = svc.Ask(context.Background(), "u", "a"); err == nil {
		t.Fatal("pregunta muy corta")
	}
}

func TestOnlySuperAdminManagesDocumentsAndSizeIsBounded(t *testing.T) {
	store := &fakeStore{role: ""}
	svc := New(store, nil, func() string { return id })
	in := domain.DocumentInput{Title: "RM 822-2018/MINSA", Content: "Artículo 1."}
	if _, err := svc.Create(context.Background(), "u", in); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("un usuario común no carga normativa: %v", err)
	}
	store.inactive = true
	if _, err := svc.Status(context.Background(), "u"); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("una cuenta inactiva no consulta: %v", err)
	}
	store.inactive, store.role = false, "SUPER_ADMIN"
	if doc, err := svc.Create(context.Background(), "u", in); err != nil || doc.ID != id {
		t.Fatalf("crear: %v %v", doc, err)
	}
	store.active = domain.MaxActiveCharacters
	if _, err := svc.Create(context.Background(), "u", in); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("debía rechazar por tamaño: %v", err)
	}
	if _, err := svc.SetStatus(context.Background(), "u", id, domain.StatusActive); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("reactivar también respeta el tope: %v", err)
	}
	if _, err := svc.SetStatus(context.Background(), "u", "no-es-uuid", domain.StatusArchived); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("id inválido: %v", err)
	}
}

func TestNormalizeDocumentValidatesFields(t *testing.T) {
	if _, err := domain.NormalizeDocument(domain.DocumentInput{Title: "", Content: ""}); err == nil {
		t.Fatal("debía exigir título y contenido")
	}
	got, err := domain.NormalizeDocument(domain.DocumentInput{Title: " NTS 142 ", Content: "a\r\nb"})
	if err != nil || got.Title != "NTS 142" || got.Content != "a\nb" {
		t.Fatalf("normalización: %+v %v", got, err)
	}
}
