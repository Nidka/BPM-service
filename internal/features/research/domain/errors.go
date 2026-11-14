package domain

import (
	"errors"
	"fmt"
)

// Errores comparables del dominio. Los adaptadores HTTP los traducen a códigos
// públicos; ningún mensaje interno llega al cliente.
var (
	ErrDenied                = errors.New("research access denied")
	ErrNotMember             = errors.New("user is not an active member of the organization")
	ErrConsentRequired       = errors.New("valid consent required")
	ErrConsentVersionStale   = errors.New("consent document version is not the active one")
	ErrConsentUnavailable    = errors.New("no active consent document")
	ErrConsentVersionTaken   = errors.New("consent document version already exists")
	ErrNoParticipant         = errors.New("participant has not enrolled")
	ErrSurveyUnavailable     = errors.New("no published CAP instrument")
	ErrVersionOutdated       = errors.New("instrument version is not the published one")
	ErrDuplicateSubmission   = errors.New("submission already exists for participant, version and moment")
	ErrIdempotencyConflict   = errors.New("operation id reused with different content")
	ErrInstrumentNotFound    = errors.New("instrument not found")
	ErrInstrumentCodeTaken   = errors.New("instrument code already exists")
	ErrCAPExists             = errors.New("a CAP instrument already exists")
	ErrDraftNotFound         = errors.New("instrument has no draft")
	ErrDraftExists           = errors.New("instrument already has a draft")
	ErrDraftConflict         = errors.New("draft modified by someone else")
	ErrVersionNotFound       = errors.New("instrument version not found")
	ErrUserNotFound          = errors.New("user not found")
	ErrGrantNotFound         = errors.New("access grant not found")
	ErrOrganizationNotFound  = errors.New("organization not found")
	ErrExportTooLarge        = errors.New("export exceeds the row limit")
	ErrParticipantCodeFailed = errors.New("could not allocate participant code")
)

// ValidationError agrupa campos inválidos de una solicitud (HTTP 422 VALIDATION_ERROR).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid research input" }

// AnswersError describe respuestas faltantes, repetidas, desconocidas o fuera de
// rango en una entrega (HTTP 422 RESEARCH_ANSWERS_INVALID).
type AnswersError struct{ Fields map[string]string }

func (e *AnswersError) Error() string { return "invalid research answers" }

// IncompleteError indica que el borrador no puede publicarse (RESEARCH_INSTRUMENT_INCOMPLETE).
type IncompleteError struct{ Fields map[string]string }

func (e *IncompleteError) Error() string { return "research instrument is incomplete" }

func invalid(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

func indexed(name string, i int, field string) string {
	return fmt.Sprintf("%s[%d].%s", name, i, field)
}
