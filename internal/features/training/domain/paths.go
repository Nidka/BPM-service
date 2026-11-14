package domain

import (
	"fmt"
	"strings"
	"time"
)

// MaxPathCourses limita los cursos de una ruta de aprendizaje.
const MaxPathCourses = 30

// PathInput es el contenido que ADMIN envía para crear o editar una ruta.
type PathInput struct {
	ID, Title, Description string
	CourseIDs              []string
	RowVersion             int
}

// Normalize valida y deja canónico el contenido de una ruta. create exige ID y
// no necesita RowVersion; al editar RowVersion es obligatorio.
func (p PathInput) Normalize(create bool) (PathInput, error) {
	fields := map[string]string{}
	p.ID = strings.ToLower(p.ID)
	p.Title = strings.TrimSpace(p.Title)
	p.Description = strings.TrimSpace(p.Description)
	if create && !ValidID(p.ID) {
		fields["id"] = "Debe ser un identificador UUID válido."
	}
	if !create && p.RowVersion < 1 {
		fields["rowVersion"] = "Falta la versión de la ruta; recárgala antes de guardar."
	}
	if !validText(p.Title, 2, MaxTitleLength, false) {
		fields["title"] = "El título debe tener entre 2 y 200 caracteres."
	}
	if !validText(p.Description, 0, 2000, true) {
		fields["description"] = "La descripción admite hasta 2000 caracteres."
	}
	if p.CourseIDs == nil {
		fields["courseIds"] = "Envía la lista ordenada de cursos (puede estar vacía)."
	} else if len(p.CourseIDs) > MaxPathCourses {
		fields["courseIds"] = "Una ruta admite hasta 30 cursos."
	} else {
		seen := map[string]bool{}
		ids := make([]string, len(p.CourseIDs))
		for i, id := range p.CourseIDs {
			id = strings.ToLower(id)
			ids[i] = id
			switch {
			case !ValidID(id):
				fields[fmt.Sprintf("courseIds[%d]", i)] = "Elige un curso válido."
			case seen[id]:
				fields[fmt.Sprintf("courseIds[%d]", i)] = "El curso está repetido en la ruta."
			}
			seen[id] = true
		}
		p.CourseIDs = ids
	}
	return p, invalid(fields)
}

// PathCourse es un curso dentro de una ruta con el avance del usuario.
type PathCourse struct {
	CourseID, Code, Title string
	Position              int
	CourseStatus          string
	Progress              string
}

// Path es una ruta de aprendizaje.
type Path struct {
	ID, Title, Description, Status string
	RowVersion                     int
	PublishedAt                    *time.Time
	Courses                        []PathCourse
}

// PathProgress es el avance calculado de una ruta.
type PathProgress struct {
	Status                         string
	CompletedCourses, TotalCourses int
	Percent                        int
	NextCourseID                   string
}

// ComputePathProgress calcula el avance contando solo cursos PUBLISHED: un
// curso aún no publicado no puede cursarse ni bloquea la ruta.
func ComputePathProgress(courses []PathCourse) PathProgress {
	out := PathProgress{Status: ProgressNotStarted}
	started := false
	for _, c := range courses {
		if c.CourseStatus != StatusPublished {
			continue
		}
		out.TotalCourses++
		switch c.Progress {
		case ProgressCompleted:
			out.CompletedCourses++
			started = true
		case ProgressInProgress:
			started = true
			if out.NextCourseID == "" {
				out.NextCourseID = c.CourseID
			}
		default:
			if out.NextCourseID == "" {
				out.NextCourseID = c.CourseID
			}
		}
	}
	if out.TotalCourses > 0 {
		out.Percent = out.CompletedCourses * 100 / out.TotalCourses
	}
	switch {
	case out.TotalCourses > 0 && out.CompletedCourses == out.TotalCourses:
		out.Status = ProgressCompleted
	case started:
		out.Status = ProgressInProgress
	}
	return out
}

// Estados de una ruta de aprendizaje.
const (
	PathDraft     = StatusDraft
	PathPublished = StatusPublished
	PathArchived  = StatusArchived
)
