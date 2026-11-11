package application

import (
	"context"
	"strconv"
	"strings"

	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// PublishVersion publica una versión nueva e inmutable de una plantilla existente. La edición se
// basa en la versión vigente (BaseVersionNumber); si otra persona publicó antes, devuelve conflicto.
func (s *Service) PublishVersion(ctx context.Context, uid, org, templateID string, in domain.NewVersion) (domain.Template, error) {
	if !uuid.MatchString(templateID) {
		return domain.Template{}, domain.ErrNotFound
	}
	templateID = strings.ToLower(templateID)
	in.ChangeNote = strings.TrimSpace(in.ChangeNote)
	in.VersionID = strings.ToLower(in.VersionID)
	fields := map[string]string{}
	if !uuid.MatchString(in.VersionID) || in.VersionID == templateID {
		fields["versionId"] = "Un UUID nuevo es obligatorio."
	}
	if in.BaseVersionNumber < 1 {
		fields["baseVersionNumber"] = "Indica la versión sobre la que editaste."
	}
	validateText(fields, "changeNote", in.ChangeNote, 0, 500)
	t := domain.Template{ID: templateID, VersionID: in.VersionID, Items: in.Items}
	validateItems(fields, &t)
	if len(fields) > 0 {
		return domain.Template{}, &domain.ValidationError{Fields: fields}
	}
	in.Items = t.Items
	hash := requestHash(struct {
		Template string
		In       domain.NewVersion
	}{templateID, in})
	var out domain.Template
	err := s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanManageTemplates(actor.Role) {
			return domain.ErrDenied
		}
		out, e = db.PublishVersion(ctx, actor, org, templateID, in, hash)
		return e
	})
	return out, err
}

// TemplateVersion devuelve una versión concreta (vigente o archivada) con sus preguntas.
func (s *Service) TemplateVersion(ctx context.Context, uid, org, templateID, versionID string) (out domain.Template, err error) {
	if !uuid.MatchString(templateID) || !uuid.MatchString(versionID) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := db.Actor(ctx, uid, org); e != nil {
			return e
		}
		var e error
		out, e = db.Version(ctx, org, strings.ToLower(templateID), strings.ToLower(versionID))
		return e
	})
	return
}

// TemplateVersions lista el historial de versiones con paginación por cursor.
func (s *Service) TemplateVersions(ctx context.Context, uid, org, templateID string, r pagination.Request) (out pagination.Page[domain.Version], err error) {
	if !uuid.MatchString(templateID) {
		return out, domain.ErrNotFound
	}
	templateID = strings.ToLower(templateID)
	scope := []string{uid, org, "checklist-versions", templateID}
	w, err := pagination.Open(r, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := db.Actor(ctx, uid, org); e != nil {
			return e
		}
		items, e := db.Versions(ctx, org, templateID, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(v domain.Version) pagination.Position {
			return pagination.Position{Key: strconv.Itoa(v.Number), ID: v.ID}
		}, scope...)
		return nil
	})
	return
}
