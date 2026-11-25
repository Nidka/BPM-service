package domain

import (
	"errors"
	"fmt"
)

// Errores comparables del dominio. Los adaptadores HTTP los traducen a códigos
// públicos; ningún mensaje interno llega al cliente.
var (
	ErrDenied              = errors.New("training access denied")
	ErrCourseNotFound      = errors.New("training course not found")
	ErrLessonNotFound      = errors.New("training lesson not found")
	ErrQuestionNotFound    = errors.New("training question not found")
	ErrVideoNotFound       = errors.New("training video not found")
	ErrDraftNotFound       = errors.New("training draft not found")
	ErrDraftConflict       = errors.New("training draft was modified by someone else")
	ErrCourseCodeTaken     = errors.New("training course code already exists")
	ErrVersionOutdated     = errors.New("training version is not the published one")
	ErrIdempotencyConflict = errors.New("training operation id reused with different content")
	ErrVideoConflict       = errors.New("training video reservation conflicts with existing data")
	ErrVideoNotReady       = errors.New("training video upload not found yet")
	ErrVideoInvalid        = errors.New("training video content differs from reservation")
	ErrUnavailable         = errors.New("private video storage unavailable")
)

// ValidationError agrupa campos inválidos de una solicitud (HTTP 422 VALIDATION_ERROR).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid training input" }

// AnswersError describe respuestas faltantes, repetidas, desconocidas o fuera de
// rango en un intento (HTTP 422 TRAINING_ANSWERS_INVALID).
type AnswersError struct{ Fields map[string]string }

func (e *AnswersError) Error() string { return "invalid training answers" }

// IncompleteError indica que el borrador no puede publicarse (TRAINING_DRAFT_INCOMPLETE).
type IncompleteError struct{ Fields map[string]string }

func (e *IncompleteError) Error() string { return "training draft is incomplete" }

func invalid(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

func indexed(name string, i int, field string) string {
	return fmt.Sprintf("%s[%d].%s", name, i, field)
}

// Errores de asignaciones, rutas, insignias y verificación pública.
var (
	ErrCourseNotPublished  = errors.New("training course is not published")
	ErrAssigneeNotFound    = errors.New("training assignee not found")
	ErrLocationNotFound    = errors.New("training location not found")
	ErrAssignmentNotFound  = errors.New("training assignment not found")
	ErrAssignmentExists    = errors.New("training assignment already exists")
	ErrCertificateNotFound = errors.New("training certificate not found")
	ErrAchievementNotFound = errors.New("training achievement not found")
	ErrPathNotFound        = errors.New("learning path not found")
	ErrPathConflict        = errors.New("learning path was modified by someone else")
	ErrPathArchived        = errors.New("learning path is archived")
)

// Errores del banco oficial de preguntas.
var (
	// ErrEvaluationNotStarted: se envió un intento de un curso con banco sin haber iniciado la evaluación.
	ErrEvaluationNotStarted = errors.New("training evaluation was not started")
	// ErrNoBank: se pidió una evaluación sorteada de un curso sin ítems del banco.
	ErrNoBank = errors.New("training course has no question bank")
)
