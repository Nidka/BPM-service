package application

import (
	"context"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// AssignmentOutcome es el resultado de crear una asignación; Replayed indica que
// el mismo id ya existía con el mismo contenido (HTTP 200 en vez de 201).
type AssignmentOutcome struct {
	Assignment domain.Assignment
	Replayed   bool
}

// CreateAssignment asigna un curso publicado a una persona, un rol o una sede.
// Solo ADMIN y SUPER_ADMIN. El tenant sale de la membresía: curso, persona y sede
// de otra empresa responden 404. Repetir el mismo id con el mismo contenido
// devuelve la misma asignación; con otro contenido, IDEMPOTENCY_CONFLICT.
// Garantiza la regla TRAINING_ASSIGNED para que el worker de alertas avise.
func (s *Service) CreateAssignment(ctx context.Context, user, org string, in domain.NewAssignment) (out AssignmentOutcome, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	now := s.now().UTC()
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		normalized, e := in.Normalize(now)
		if e != nil {
			return e
		}
		existing, found, e := db.FindAssignment(ctx, org, normalized.ID)
		if e != nil {
			return e
		}
		if found {
			if !existing.NewAssignment.SameAs(normalized) {
				return domain.ErrIdempotencyConflict
			}
			out = AssignmentOutcome{Assignment: existing, Replayed: true}
			return nil
		}
		if _, e = db.PublishedCourse(ctx, org, normalized.CourseID); e != nil {
			return e
		}
		if e = db.ResolveTarget(ctx, org, normalized.Target); e != nil {
			return e
		}
		// La regla debe existir antes que la asignación para que el evaluador la considere.
		if e = db.EnsureAssignmentAlertRule(ctx, org, user); e != nil {
			return e
		}
		created, e := db.InsertAssignment(ctx, org, user, normalized)
		if e != nil {
			return e
		}
		out = AssignmentOutcome{Assignment: created}
		return db.Audit(ctx, org, user, "training.assignment.created", "training_assignment", created.ID)
	})
	return out, err
}

// ListAssignments lista las asignaciones de la empresa con paginación por cursor.
func (s *Service) ListAssignments(ctx context.Context, user, org string, filter domain.AssignmentFilter, req pagination.Request) (page pagination.Page[domain.Assignment], err error) {
	if !domain.ValidOrganization(org) {
		return page, domain.ErrCourseNotFound
	}
	filter.CourseID = strings.ToLower(filter.CourseID)
	scope := []string{user, org, "training-assignments", filter.Status, filter.CourseID}
	w, err := pagination.Open(req, scope...)
	if err != nil {
		return page, err
	}
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return page, pagination.ErrInvalid
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		if filter.Status != "" && !domain.ValidAssignmentStatus(filter.Status) {
			return &domain.ValidationError{Fields: map[string]string{"status": "El estado debe ser ACTIVE o CANCELLED."}}
		}
		if filter.CourseID != "" && !domain.ValidID(filter.CourseID) {
			return &domain.ValidationError{Fields: map[string]string{"courseId": "Elige un curso válido."}}
		}
		items, e := db.ListAssignments(ctx, org, filter, w)
		if e != nil {
			return e
		}
		page = pagination.Close(items, w, func(a domain.Assignment) pagination.Position {
			return pagination.Position{Key: a.CreatedAt.UTC().Format(time.RFC3339Nano), ID: a.ID}
		}, scope...)
		return nil
	})
	return page, err
}

// CancelAssignment cancela una asignación; repetirlo es idempotente. Una
// asignación de otra empresa responde 404.
func (s *Service) CancelAssignment(ctx context.Context, user, org, id string) (out domain.Assignment, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	id = strings.ToLower(id)
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		if !domain.ValidID(id) {
			return domain.ErrAssignmentNotFound
		}
		var e error
		if out, e = db.CancelAssignment(ctx, org, user, id, s.now().UTC()); e != nil {
			return e
		}
		return db.Audit(ctx, org, user, "training.assignment.cancelled", "training_assignment", id)
	})
	return out, err
}

// MyAssignments lista las capacitaciones asignadas al usuario con el estado
// calculado por el servidor desde su progreso (pendiente, en curso, completada o vencida).
func (s *Service) MyAssignments(ctx context.Context, user, org string, req pagination.Request) (page pagination.Page[domain.MyAssignment], err error) {
	if !domain.ValidOrganization(org) {
		return page, domain.ErrCourseNotFound
	}
	scope := []string{user, org, "training-my-assignments"}
	w, err := pagination.Open(req, scope...)
	if err != nil {
		return page, err
	}
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return page, pagination.ErrInvalid
	}
	now := s.now().UTC()
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, persister); e != nil {
			return e
		}
		items, e := db.MyAssignments(ctx, org, user, w)
		if e != nil {
			return e
		}
		for i := range items {
			items[i].Status = domain.LearnStatus(items[i].ProgressStatus, items[i].DueAt, now)
		}
		page = pagination.Close(items, w, func(a domain.MyAssignment) pagination.Position {
			return pagination.Position{Key: a.AssignedAt.UTC().Format(time.RFC3339Nano), ID: a.AssignmentID}
		}, scope...)
		return nil
	})
	return page, err
}
