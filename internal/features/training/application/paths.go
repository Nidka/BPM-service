package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// PathOutcome es una ruta con el avance del usuario; Replayed indica una creación repetida.
type PathOutcome struct {
	Path     domain.Path
	Progress domain.PathProgress
	Replayed bool
}

func pathKey(org, id string) (string, error) {
	id = strings.ToLower(id)
	if !domain.ValidOrganization(org) || !domain.ValidID(id) {
		return "", domain.ErrPathNotFound
	}
	return id, nil
}

// progressUser devuelve el usuario cuyo progreso se calcula; SUPER_ADMIN no tiene membresía.
func progressUser(a domain.Actor, user string) string {
	if a.CanPersist() {
		return user
	}
	return ""
}

// checkPathCourses exige que cada curso exista en la organización y, si la ruta
// debe estar publicada, que el curso también lo esté. Los cursos ajenos no existen.
func checkPathCourses(ctx context.Context, db Store, org string, ids []string, requirePublished bool) error {
	if len(ids) == 0 {
		return nil
	}
	statuses, err := db.CourseStatuses(ctx, org, ids)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	for i, id := range ids {
		status, ok := statuses[id]
		switch {
		case !ok:
			fields[fmt.Sprintf("courseIds[%d]", i)] = "El curso no existe en esta empresa."
		case requirePublished && status != domain.StatusPublished:
			fields[fmt.Sprintf("courseIds[%d]", i)] = "El curso aún no está publicado."
		}
	}
	if len(fields) > 0 {
		return &domain.ValidationError{Fields: fields}
	}
	return nil
}

// CreatePath crea una ruta en DRAFT. Idempotente por id: repetir el mismo contenido
// devuelve la misma ruta; otro contenido con el mismo id es IDEMPOTENCY_CONFLICT.
func (s *Service) CreatePath(ctx context.Context, user, org string, in domain.PathInput) (out PathOutcome, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrPathNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := s.actor(ctx, db, user, org, author)
		if e != nil {
			return e
		}
		normalized, e := in.Normalize(true)
		if e != nil {
			return e
		}
		existing, found, e := db.FindPath(ctx, org, progressUser(a, user), normalized.ID, true)
		if e != nil {
			return e
		}
		if found {
			if !samePath(existing, normalized) {
				return domain.ErrIdempotencyConflict
			}
			out = PathOutcome{Path: existing, Progress: domain.ComputePathProgress(existing.Courses), Replayed: true}
			return nil
		}
		if e = checkPathCourses(ctx, db, org, normalized.CourseIDs, false); e != nil {
			return e
		}
		if e = db.InsertPath(ctx, org, user, normalized); e != nil {
			return e
		}
		created, _, e := db.FindPath(ctx, org, progressUser(a, user), normalized.ID, false)
		if e != nil {
			return e
		}
		out = PathOutcome{Path: created, Progress: domain.ComputePathProgress(created.Courses)}
		return db.Audit(ctx, org, user, "training.path.created", "learning_path", created.ID)
	})
	return out, err
}

func samePath(p domain.Path, in domain.PathInput) bool {
	if p.Title != in.Title || p.Description != in.Description || len(p.Courses) != len(in.CourseIDs) {
		return false
	}
	for i, c := range p.Courses {
		if c.CourseID != in.CourseIDs[i] {
			return false
		}
	}
	return true
}

// UpdatePath reemplaza título, descripción y secuencia de cursos con control de
// versión de fila (ErrPathConflict). Una ruta archivada no se edita.
func (s *Service) UpdatePath(ctx context.Context, user, org, pathID string, in domain.PathInput) (out PathOutcome, err error) {
	if pathID, err = pathKey(org, pathID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := s.actor(ctx, db, user, org, author)
		if e != nil {
			return e
		}
		normalized, e := in.Normalize(false)
		if e != nil {
			return e
		}
		current, found, e := db.FindPath(ctx, org, "", pathID, true)
		if e != nil {
			return e
		}
		if !found {
			return domain.ErrPathNotFound
		}
		if current.Status == domain.PathArchived {
			return domain.ErrPathArchived
		}
		if e = checkPathCourses(ctx, db, org, normalized.CourseIDs, current.Status == domain.PathPublished); e != nil {
			return e
		}
		if current.Status == domain.PathPublished && len(normalized.CourseIDs) == 0 {
			return &domain.ValidationError{Fields: map[string]string{"courseIds": "Una ruta publicada necesita al menos un curso."}}
		}
		if e = db.ReplacePath(ctx, org, user, pathID, normalized.RowVersion, normalized); e != nil {
			return e
		}
		updated, _, e := db.FindPath(ctx, org, progressUser(a, user), pathID, false)
		if e != nil {
			return e
		}
		out = PathOutcome{Path: updated, Progress: domain.ComputePathProgress(updated.Courses)}
		return db.Audit(ctx, org, user, "training.path.updated", "learning_path", pathID)
	})
	return out, err
}

// PublishPath publica una ruta con al menos un curso y todos sus cursos publicados.
// Repetirlo es idempotente.
func (s *Service) PublishPath(ctx context.Context, user, org, pathID string) (PathOutcome, error) {
	return s.transitionPath(ctx, user, org, pathID, domain.PathPublished)
}

// ArchivePath retira una ruta; repetirlo es idempotente.
func (s *Service) ArchivePath(ctx context.Context, user, org, pathID string) (PathOutcome, error) {
	return s.transitionPath(ctx, user, org, pathID, domain.PathArchived)
}

func (s *Service) transitionPath(ctx context.Context, user, org, pathID, target string) (out PathOutcome, err error) {
	if pathID, err = pathKey(org, pathID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := s.actor(ctx, db, user, org, author)
		if e != nil {
			return e
		}
		current, found, e := db.FindPath(ctx, org, "", pathID, true)
		if e != nil {
			return e
		}
		if !found {
			return domain.ErrPathNotFound
		}
		if current.Status != target {
			if target == domain.PathPublished {
				if current.Status == domain.PathArchived {
					return domain.ErrPathArchived
				}
				if len(current.Courses) == 0 {
					return &domain.IncompleteError{Fields: map[string]string{"courseIds": "Agrega al menos un curso antes de publicar la ruta."}}
				}
				ids := make([]string, len(current.Courses))
				for i, c := range current.Courses {
					ids[i] = c.CourseID
				}
				if e = checkPathCourses(ctx, db, org, ids, true); e != nil {
					return &domain.IncompleteError{Fields: validationFields(e)}
				}
			}
			if e = db.SetPathStatus(ctx, org, user, pathID, target, s.now().UTC()); e != nil {
				return e
			}
			if e = db.Audit(ctx, org, user, "training.path."+strings.ToLower(target), "learning_path", pathID); e != nil {
				return e
			}
		}
		updated, _, e := db.FindPath(ctx, org, progressUser(a, user), pathID, false)
		if e != nil {
			return e
		}
		out = PathOutcome{Path: updated, Progress: domain.ComputePathProgress(updated.Courses)}
		return nil
	})
	return out, err
}

func validationFields(err error) map[string]string {
	if v, ok := err.(*domain.ValidationError); ok {
		return v.Fields
	}
	return map[string]string{"courseIds": "Revisa los cursos de la ruta."}
}

// Path devuelve una ruta. Quien aprende solo ve rutas PUBLISHED; ADMIN ve todas.
func (s *Service) Path(ctx context.Context, user, org, pathID string) (out PathOutcome, err error) {
	if pathID, err = pathKey(org, pathID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := s.actor(ctx, db, user, org, learner)
		if e != nil {
			return e
		}
		path, found, e := db.FindPath(ctx, org, progressUser(a, user), pathID, false)
		if e != nil {
			return e
		}
		if !found || (path.Status != domain.PathPublished && !a.CanAuthor()) {
			return domain.ErrPathNotFound
		}
		out = PathOutcome{Path: path, Progress: domain.ComputePathProgress(path.Courses)}
		return nil
	})
	return out, err
}

// PathPage es una página de rutas con el avance del usuario.
type PathPage struct {
	Items     []PathOutcome
	NextToken string
}

// ListPaths lista rutas. Quien aprende solo ve las publicadas; ADMIN puede filtrar
// por estado (DRAFT, PUBLISHED, ARCHIVED).
func (s *Service) ListPaths(ctx context.Context, user, org, status string, req pagination.Request) (out PathPage, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrPathNotFound
	}
	scope := []string{user, org, "training-paths", status}
	w, err := pagination.Open(req, scope...)
	if err != nil {
		return out, err
	}
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return out, pagination.ErrInvalid
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := s.actor(ctx, db, user, org, learner)
		if e != nil {
			return e
		}
		if status != "" && !domain.ValidCourseStatus(status) {
			return &domain.ValidationError{Fields: map[string]string{"status": "El estado debe ser DRAFT, PUBLISHED o ARCHIVED."}}
		}
		if !a.CanAuthor() {
			if status != "" && status != domain.PathPublished {
				return domain.ErrDenied
			}
			status = domain.PathPublished
		}
		items, e := db.ListPaths(ctx, org, progressUser(a, user), status, w)
		if e != nil {
			return e
		}
		page := pagination.Close(items, w, func(p domain.Path) pagination.Position {
			return pagination.Position{Key: p.Title, ID: p.ID}
		}, scope...)
		out.NextToken = page.NextToken
		for _, p := range page.Items {
			out.Items = append(out.Items, PathOutcome{Path: p, Progress: domain.ComputePathProgress(p.Courses)})
		}
		return nil
	})
	return out, err
}
