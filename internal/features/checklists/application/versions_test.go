package application

import (
	"context"
	"errors"
	"testing"

	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

var published bool

func (f *fakeStore) PublishVersion(context.Context, domain.Actor, string, string, domain.NewVersion, string) (domain.Template, error) {
	published = true
	return domain.Template{}, nil
}
func (f *fakeStore) Version(context.Context, string, string, string) (domain.Template, error) {
	return domain.Template{}, nil
}
func (f *fakeStore) Versions(context.Context, string, string, pagination.Window) ([]domain.Version, error) {
	return nil, nil
}

func TestPublishVersionRoleAndValidation(t *testing.T) {
	tpl, ver := "10000000-0000-0000-0000-0000000000aa", "30000000-0000-0000-0000-0000000000bb"
	item := domain.Item{ID: "40000000-0000-0000-0000-0000000000cc", SectionName: "Higiene", Label: "Lavado de manos", ResponseType: "COMPLIANCE", DisplayOrder: 1, Required: true}
	good := domain.NewVersion{VersionID: ver, BaseVersionNumber: 1, Items: []domain.Item{item}}
	f := &fakeStore{actor: domain.Actor{ID: "chef", Role: "CHEF"}}
	s := New(fakeWork{f})
	if _, e := s.PublishVersion(context.Background(), "chef", "org", tpl, good); !errors.Is(e, domain.ErrDenied) || published {
		t.Fatal("un chef publicó una versión", e)
	}
	f.actor = domain.Actor{ID: "admin", Role: "ADMIN"}
	for name, in := range map[string]domain.NewVersion{
		"sin base":      {VersionID: ver, Items: good.Items},
		"sin preguntas": {VersionID: ver, BaseVersionNumber: 1},
		"mismo id":      {VersionID: tpl, BaseVersionNumber: 1, Items: good.Items},
	} {
		var v *domain.ValidationError
		if _, e := s.PublishVersion(context.Background(), "admin", "org", tpl, in); !errors.As(e, &v) {
			t.Fatal(name, e)
		}
	}
	if _, e := s.PublishVersion(context.Background(), "admin", "org", "x", good); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.PublishVersion(context.Background(), "admin", "org", tpl, good); e != nil || !published {
		t.Fatal(e)
	}
}
