package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// maxNextCourseAssignments limita las asignaciones que se revisan para sugerir un curso.
const maxNextCourseAssignments = 100

// nextCourse sugiere el siguiente curso tras aprobar un FINAL: primero por las rutas
// publicadas que contienen el curso y, si ninguna propone uno, por las asignaciones pendientes.
func (s *Service) nextCourse(ctx context.Context, db Store, org, user, courseID string, now time.Time) (*domain.NextCourse, error) {
	paths, err := db.PublishedPathsWithCourse(ctx, org, user, courseID)
	if err != nil {
		return nil, err
	}
	if next := domain.SelectNextCourse(courseID, paths, nil); next != nil {
		return next, nil
	}
	assignments, err := db.MyAssignments(ctx, org, user, pagination.Window{Size: maxNextCourseAssignments})
	if err != nil {
		return nil, err
	}
	for i := range assignments {
		assignments[i].Status = domain.LearnStatus(assignments[i].ProgressStatus, assignments[i].DueAt, now)
	}
	return domain.SelectNextCourse(courseID, nil, assignments), nil
}
