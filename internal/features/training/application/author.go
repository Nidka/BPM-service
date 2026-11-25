package application

import (
	"context"
	"fmt"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// ListCourses lista hasta 100 cursos para administrar, ordenados por título.
func (s *Service) ListCourses(ctx context.Context, user, org, status string) (out []domain.CourseSummary, err error) {
	if !domain.ValidOrganization(org) {
		return nil, domain.ErrCourseNotFound
	}
	if status != "" && !domain.ValidCourseStatus(status) {
		return nil, &domain.ValidationError{Fields: map[string]string{"status": "El estado debe ser DRAFT, PUBLISHED o ARCHIVED."}}
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		var e error
		out, e = db.ListCourses(ctx, org, status)
		return e
	})
	return out, err
}

// CreateCourse crea un curso DRAFT; el código es único dentro de la organización.
func (s *Service) CreateCourse(ctx context.Context, user, org string, in domain.NewCourse) (out domain.Course, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		normalized, e := in.Normalize()
		if e != nil {
			return e
		}
		if out, e = db.CreateCourse(ctx, org, user, normalized); e != nil {
			return e
		}
		return db.Audit(ctx, org, user, "training.course.created", "training_course", out.ID)
	})
	return out, err
}

// Draft devuelve el borrador editable; lo crea si aún no existe, copiando la
// versión publicada (con identificadores nuevos) o vacío si el curso es nuevo.
func (s *Service) Draft(ctx context.Context, user, org, courseID string) (out domain.Draft, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		course, e := db.LockCourse(ctx, org, courseID)
		if e != nil {
			return e
		}
		draft, found, e := db.Draft(ctx, org, course)
		if e != nil {
			return e
		}
		if !found {
			if draft, e = db.CreateDraft(ctx, org, user, course); e != nil {
				return e
			}
			if e = db.Audit(ctx, org, user, "training.draft.created", "training_course", courseID); e != nil {
				return e
			}
		}
		out = draft
		return nil
	})
	return out, err
}

// SaveDraft reemplaza lecciones y preguntas del borrador con control optimista:
// un rowVersion desactualizado produce ErrDraftConflict.
func (s *Service) SaveDraft(ctx context.Context, user, org, courseID string, in domain.DraftInput) (out domain.Draft, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		// La autorización precede a la validación: quien no administra no aprende nada del formato.
		in, e := domain.NormalizeDraft(in)
		if e != nil {
			return e
		}
		course, e := db.LockCourse(ctx, org, courseID)
		if e != nil {
			return e
		}
		draft, found, e := db.Draft(ctx, org, course)
		if e != nil {
			return e
		}
		if !found {
			return domain.ErrDraftNotFound
		}
		if (in.VersionID != "" && in.VersionID != draft.Version.ID) || in.RowVersion != draft.Version.RowVersion {
			return domain.ErrDraftConflict
		}
		if e = s.checkVideos(ctx, db, org, in.Lessons); e != nil {
			return e
		}
		in.Questions = keepBankItems(draft.Questions, in.Questions)
		in.Lessons = keepPlatformVideos(draft.Lessons, in.Lessons)
		in.Lessons = keepKeyPoints(draft.Lessons, in.Lessons)
		in.Lessons = keepCaptions(draft.Lessons, in.Lessons)
		if out, e = db.ReplaceDraft(ctx, org, user, draft, in); e != nil {
			return e
		}
		return db.Audit(ctx, org, user, "training.draft.saved", "training_course", courseID)
	})
	return out, err
}

// keepKeyPoints conserva "Lo esencial" de las lecciones que el cliente no envió (KeyPoints nil,
// por ejemplo una app anterior que no conoce el campo); un arreglo vacío sí lo borra. Las
// lecciones nuevas quedan con un arreglo vacío. No modifica la lista recibida.
func keepKeyPoints(previous, next []domain.Lesson) []domain.Lesson {
	before := make(map[string][]string, len(previous))
	for _, l := range previous {
		before[l.ID] = l.KeyPoints
	}
	out := make([]domain.Lesson, len(next))
	copy(out, next)
	for i := range out {
		if out[i].KeyPoints == nil {
			out[i].KeyPoints = before[out[i].ID]
		}
		if out[i].KeyPoints == nil {
			out[i].KeyPoints = []string{}
		}
	}
	return out
}

// keepCaptions conserva los subtítulos (RF-45) de las lecciones que el cliente no envió (Captions nil,
// por ejemplo una app anterior que no conoce el campo); "" sí los borra. No modifica la lista recibida.
func keepCaptions(previous, next []domain.Lesson) []domain.Lesson {
	before := make(map[string]*string, len(previous))
	for _, l := range previous {
		before[l.ID] = l.Captions
	}
	out := make([]domain.Lesson, len(next))
	copy(out, next)
	empty := ""
	for i := range out {
		if out[i].Captions == nil {
			out[i].Captions = before[out[i].ID]
		}
		if out[i].Captions == nil {
			out[i].Captions = &empty
		}
	}
	return out
}

// checkVideos exige que cada video referenciado sea un video de capacitación
// AVAILABLE o PENDING_UPLOAD de la misma organización.
func (s *Service) checkVideos(ctx context.Context, db Store, org string, lessons []domain.Lesson) error {
	ids := videoIDs(lessons)
	if len(ids) == 0 {
		return nil
	}
	statuses, err := db.VideoStatuses(ctx, org, ids)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	for i, l := range lessons {
		if l.VideoMediaID == "" {
			continue
		}
		if st := statuses[l.VideoMediaID]; st != "AVAILABLE" && st != "PENDING_UPLOAD" {
			fields[fmt.Sprintf("lessons[%d].videoMediaId", i)] = "El video no existe en esta organización o ya no está disponible."
		}
	}
	if len(fields) > 0 {
		return &domain.ValidationError{Fields: fields}
	}
	return nil
}

func videoIDs(lessons []domain.Lesson) []string {
	seen := map[string]bool{}
	var ids []string
	for _, l := range lessons {
		if l.VideoMediaID != "" && !seen[l.VideoMediaID] {
			seen[l.VideoMediaID] = true
			ids = append(ids, l.VideoMediaID)
		}
	}
	return ids
}

// PublishResult resume una publicación.
type PublishResult struct {
	Course  domain.Course
	Version domain.Version
}

// Publish convierte el borrador en la versión PUBLISHED y archiva la anterior.
// Requiere al menos una lección, una pregunta y todos los videos AVAILABLE.
func (s *Service) Publish(ctx context.Context, user, org, courseID string) (out PublishResult, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		course, e := db.LockCourse(ctx, org, courseID)
		if e != nil {
			return e
		}
		draft, found, e := db.Draft(ctx, org, course)
		if e != nil {
			return e
		}
		if !found {
			return domain.ErrDraftNotFound
		}
		statuses := map[string]string{}
		if ids := videoIDs(draft.Lessons); len(ids) > 0 {
			if statuses, e = db.VideoStatuses(ctx, org, ids); e != nil {
				return e
			}
		}
		if e = draft.ValidateForPublish(statuses); e != nil {
			return e
		}
		now := s.now().UTC()
		if out.Course, out.Version, e = db.PublishDraft(ctx, org, user, draft, now); e != nil {
			return e
		}
		return db.Audit(ctx, org, user, "training.course.published", "training_course", courseID)
	})
	return out, err
}

// keepPlatformVideos conserva el video de la plataforma de las lecciones que el cliente no envía con otro video
// propio (privado o de YouTube): el cliente no conoce ese campo. Una lección con video propio lo reemplaza.
func keepPlatformVideos(previous, next []domain.Lesson) []domain.Lesson {
	before := make(map[string]string, len(previous))
	for _, l := range previous {
		if l.PlatformVideoID != "" {
			before[l.ID] = l.PlatformVideoID
		}
	}
	out := make([]domain.Lesson, len(next))
	copy(out, next)
	for i := range out {
		if id := before[out[i].ID]; id != "" && out[i].VideoMediaID == "" && out[i].ExternalVideo == nil {
			out[i].PlatformVideoID = id
		}
	}
	return out
}
