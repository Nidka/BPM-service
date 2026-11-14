package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// MaxOfflineCourses limita el tamaño del paquete offline.
const MaxOfflineCourses = 50

// OfflineCatalog es el paquete descargable con los cursos asignados al usuario.
// ETag cambia si cambia cualquier contenido incluido; el cliente lo usa para cachear.
type OfflineCatalog struct {
	ETag        string
	GeneratedAt time.Time
	Courses     []domain.CourseView
}

// OfflineCatalog reúne en un paquete los cursos publicados asignados al usuario:
// lecciones, preguntas sin respuestas correctas ni explicaciones y metadatos de
// video privado (sin URL). Los videos externos no son descargables.
func (s *Service) OfflineCatalog(ctx context.Context, user, org string) (out OfflineCatalog, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, persister); e != nil {
			return e
		}
		ids, e := db.AssignedCourseIDs(ctx, org, user, MaxOfflineCourses)
		if e != nil {
			return e
		}
		sort.Strings(ids)
		out.Courses = []domain.CourseView{}
		for _, id := range ids {
			// userID vacío: el paquete es contenido, no progreso, y así el etag solo cambia con el contenido.
			view, e := db.CourseView(ctx, org, "", id)
			if e != nil {
				return e
			}
			out.Courses = append(out.Courses, view)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(out.Courses)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(raw)
	out.ETag = hex.EncodeToString(sum[:])
	out.GeneratedAt = s.now().UTC()
	return out, nil
}
