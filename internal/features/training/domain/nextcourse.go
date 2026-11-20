package domain

import "sort"

// NextCourse es el curso que se sugiere tras aprobar un FINAL, con la ruta que lo propone.
// PathID y PathTitle quedan vacíos cuando proviene de las asignaciones pendientes.
type NextCourse struct {
	PathID, PathTitle, CourseID, CourseTitle string
}

// SelectNextCourse elige el siguiente curso tras aprobar currentCourseID. Primero entre las
// rutas PUBLISHED que contienen el curso: la ruta propone su próximo curso pendiente y solo
// cuenta si es distinto del actual (una ruta ya completa no propone nada); si varias
// rutas proponen, gana la publicada más recientemente. Si ninguna ruta propone, se usa la
// primera asignación PENDING u OVERDUE (en el orden recibido) que no sea el curso actual.
// Sin ninguna, devuelve nil. Las rutas deben traer el progreso de la persona.
func SelectNextCourse(currentCourseID string, paths []Path, assignments []MyAssignment) *NextCourse {
	ordered := append([]Path(nil), paths...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].PublishedAt, ordered[j].PublishedAt
		if (a == nil) != (b == nil) {
			return a != nil
		}
		if a != nil && !a.Equal(*b) {
			return a.After(*b)
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, p := range ordered {
		if p.Status != PathPublished || !pathHasCourse(p, currentCourseID) {
			continue
		}
		next := ComputePathProgress(p.Courses).NextCourseID
		if next == "" || next == currentCourseID {
			continue
		}
		for _, c := range p.Courses {
			if c.CourseID == next {
				return &NextCourse{PathID: p.ID, PathTitle: p.Title, CourseID: c.CourseID, CourseTitle: c.Title}
			}
		}
	}
	for _, a := range assignments {
		if a.Course.ID != currentCourseID && (a.Status == LearnPending || a.Status == LearnOverdue) {
			return &NextCourse{CourseID: a.Course.ID, CourseTitle: a.Course.Title}
		}
	}
	return nil
}

func pathHasCourse(p Path, courseID string) bool {
	for _, c := range p.Courses {
		if c.CourseID == courseID {
			return true
		}
	}
	return false
}
